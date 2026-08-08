package agentic

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
)

// fakeModel replays one canned response per call, so a test can script exactly
// what a provider would send back without a network call.
type fakeModel struct {
	replies [][]*model.Response
	calls   []*model.Request
}

func (f *fakeModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	f.calls = append(f.calls, req)
	idx := len(f.calls) - 1
	if idx >= len(f.replies) {
		idx = len(f.replies) - 1
	}
	chunks := f.replies[idx]
	ch := make(chan *model.Response, len(chunks))
	for _, r := range chunks {
		ch <- r
	}
	close(ch)
	return ch, nil
}

func (f *fakeModel) Info() model.Info { return model.Info{Name: "fake"} }

func lengthReason() *string { s := "length"; return &s }

// chunk builds a single streamed response carrying a text delta.
func chunk(text string, done bool, finish *string) *model.Response {
	return &model.Response{
		Done: done,
		Choices: []model.Choice{{
			Delta:        model.Message{Content: text},
			FinishReason: finish,
		}},
	}
}

func testEntry(m model.Model, maxOutputTokens int) models.Entry {
	return models.Entry{Model: m, Name: "fake", MaxOutputTokens: maxOutputTokens}
}

// TestCompleteJSONRetriesOnTruncation reproduces the bug reported from a live
// run: the recommend node's reply was cut off mid-string by the provider's
// output-token budget, and decodeJSON rejected it as "not valid JSON" even
// though the model had not actually gotten anything wrong. completeJSON must
// recognise finish_reason "length" and retry with a larger budget instead of
// failing the node outright.
func TestCompleteJSONRetriesOnTruncation(t *testing.T) {
	truncated := `{"recommendations":[{"change_id":20,"severity":"critical","risk":"terpotong di sini`
	full := `{"recommendations":[{"change_id":20,"severity":"critical","risk":"lengkap"}]}`

	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(truncated, true, lengthReason())},
		{chunk(full, true, nil)},
	}}

	c := caller{entry: testEntry(fm, 100), node: "recommend", usage: &Usage{}}

	var out struct {
		Recommendations []struct {
			ChangeID int    `json:"change_id"`
			Severity string `json:"severity"`
			Risk     string `json:"risk"`
		} `json:"recommendations"`
	}
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	if len(out.Recommendations) != 1 || out.Recommendations[0].Risk != "lengkap" {
		t.Fatalf("unexpected decoded result: %+v", out)
	}
	if len(fm.calls) != 2 {
		t.Fatalf("expected a retry after truncation, got %d calls", len(fm.calls))
	}
	first, second := *fm.calls[0].MaxTokens, *fm.calls[1].MaxTokens
	if second <= first {
		t.Fatalf("expected the retry to raise the token budget, got %d then %d", first, second)
	}
}

// TestCompleteJSONReportsTruncationAfterRetry checks that a reply still
// truncated after the retry produces an error naming the real cause (the
// output-token budget) rather than the generic "invalid JSON" message, which
// would send anyone debugging it looking in the wrong place.
func TestCompleteJSONReportsTruncationAfterRetry(t *testing.T) {
	truncated := `{"recommendations":[{"change_id":20,"risk":"masih terpotong`

	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(truncated, true, lengthReason())},
		{chunk(truncated, true, lengthReason())},
	}}

	c := caller{entry: testEntry(fm, 100), node: "recommend", usage: &Usage{}}

	var out map[string]any
	err := c.completeJSON(context.Background(), "system", "user", &out, "desc")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "terpotong") {
		t.Fatalf("expected the error to name truncation, got: %v", err)
	}
	if len(fm.calls) != 2 {
		t.Fatalf("expected exactly one retry, got %d calls", len(fm.calls))
	}
}

// TestCompleteJSONDoesNotRetryOnGenuinelyMalformedReply checks that a reply
// that simply is not JSON — no truncation signal at all — fails on the first
// attempt instead of burning a second call that was never going to help.
func TestCompleteJSONDoesNotRetryOnGenuinelyMalformedReply(t *testing.T) {
	fm := &fakeModel{replies: [][]*model.Response{
		{chunk("maaf, saya tidak bisa membantu.", true, nil)},
	}}

	c := caller{entry: testEntry(fm, 100), node: "recommend", usage: &Usage{}}

	var out map[string]any
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err == nil {
		t.Fatal("expected an error, got nil")
	}
	if len(fm.calls) != 1 {
		t.Fatalf("expected no retry for a non-truncated malformed reply, got %d calls", len(fm.calls))
	}
}

// TestCompleteJSONUsesConfiguredMaxTokens checks that the tier's registry
// budget reaches the request, since an unset MaxTokens is what let the
// Anthropic adapter's own 4096-token fallback silently truncate replies the
// prompts had already grown too large for.
func TestCompleteJSONUsesConfiguredMaxTokens(t *testing.T) {
	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(`{"ok":true}`, true, nil)},
	}}
	c := caller{entry: testEntry(fm, 16384), node: "recommend", usage: &Usage{}}

	var out map[string]any
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	if got := *fm.calls[0].MaxTokens; got != 16384 {
		t.Fatalf("expected MaxTokens=16384 from the registry entry, got %d", got)
	}
}

// TestCompleteJSONFallsBackToDefaultMaxTokens checks a tier left unconfigured
// still gets an explicit budget rather than relying on the adapter's default,
// matching what the adapter would have picked anyway.
func TestCompleteJSONFallsBackToDefaultMaxTokens(t *testing.T) {
	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(`{"ok":true}`, true, nil)},
	}}
	c := caller{entry: testEntry(fm, 0), node: "triage", usage: &Usage{}}

	var out map[string]any
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	if got := *fm.calls[0].MaxTokens; got != defaultMaxOutputTokens {
		t.Fatalf("expected MaxTokens=%d, got %d", defaultMaxOutputTokens, got)
	}
}
