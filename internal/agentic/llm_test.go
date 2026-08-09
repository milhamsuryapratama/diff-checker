package agentic

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

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

// TestCompleteJSONEnablesThinking reproduces why the trace UI's "thinking"
// panel showed nothing but tool calls and one-line notes: every request built
// in this package left model.Request.ThinkingEnabled at its zero value (nil),
// which the Anthropic adapter reads as "never ask the model to think" rather
// than "no preference" — so no tier ever received a reasoning block to
// display, regardless of how well the display code itself worked.
func TestCompleteJSONEnablesThinking(t *testing.T) {
	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(`{"ok":true}`, true, nil)},
	}}
	c := caller{entry: testEntry(fm, 4096), node: "recommend", usage: &Usage{}}
	c.entry.ReasoningEffort = "high"

	var out map[string]any
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	req := fm.calls[0]
	if req.ThinkingEnabled == nil || !*req.ThinkingEnabled {
		t.Fatal("expected ThinkingEnabled to be set to true")
	}
	if req.ReasoningEffort == nil || *req.ReasoningEffort != "high" {
		t.Fatalf("expected ReasoningEffort=high, got %v", req.ReasoningEffort)
	}
}

// TestCompleteJSONLeavesReasoningEffortUnsetWhenTierHasNone checks a tier like
// triage, whose model does not support adaptive thinking, still turns
// thinking on without forcing an effort value the model can't use.
func TestCompleteJSONLeavesReasoningEffortUnsetWhenTierHasNone(t *testing.T) {
	fm := &fakeModel{replies: [][]*model.Response{
		{chunk(`{"ok":true}`, true, nil)},
	}}
	c := caller{entry: testEntry(fm, 2048), node: "triage", usage: &Usage{}}

	var out map[string]any
	if err := c.completeJSON(context.Background(), "system", "user", &out, "desc"); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	req := fm.calls[0]
	if req.ThinkingEnabled == nil || !*req.ThinkingEnabled {
		t.Fatal("expected ThinkingEnabled to be set to true even with no configured effort")
	}
	if req.ReasoningEffort != nil {
		t.Fatalf("expected ReasoningEffort to stay unset, got %v", *req.ReasoningEffort)
	}
}

// TestToolLoopIntermediateRoundsDoNotRequestThinking guards the reason
// thinking is deliberately left off mid-loop: the adapter drops
// ReasoningContent when it re-serialises a tool-use turn back into
// conversation history, and Anthropic requires that turn's thinking block to
// survive the round trip. Requesting thinking on an intermediate round would
// make the *next* round fail outright once a real API is involved, so the
// first (tool-calling) round must go out without it while the final
// answer-only round may still ask for it.
func TestToolLoopIntermediateRoundsDoNotRequestThinking(t *testing.T) {
	toolCallMsg := &model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "get_paragraph",
					Arguments: []byte(`{"index":1}`),
				},
			}},
		}}},
		Done: true,
	}
	finalMsg := &model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleAssistant,
			Content: `{"ok":true}`,
		}}},
		Done: true,
	}

	fm := &fakeModel{replies: [][]*model.Response{
		{toolCallMsg}, // round 1: tool-calling round, non-streamed
		{finalMsg},    // round 2: no more tool calls -> triggers finalJSON
		{chunk(`{"ok":true}`, true, nil)}, // finalJSON's own streamed call
	}}
	c := caller{entry: testEntry(fm, 4096), node: "analyze", usage: &Usage{}}
	c.entry.ReasoningEffort = "medium"

	var out map[string]any
	err := c.completeJSONWithTools(context.Background(), "system", "user", map[string]tool.Tool{}, &out, "desc")
	if err != nil {
		t.Fatalf("completeJSONWithTools: %v", err)
	}
	if len(fm.calls) != 3 {
		t.Fatalf("expected 3 calls (2 tool rounds + finalJSON), got %d", len(fm.calls))
	}
	if got := fm.calls[0].ThinkingEnabled; got != nil {
		t.Fatalf("round 1 (has tool calls) must not request thinking, got %v", *got)
	}
	if got := fm.calls[1].ThinkingEnabled; got != nil {
		t.Fatalf("round 2 (no tool calls, still non-streamed) must not request thinking, got %v", *got)
	}
	if got := fm.calls[2].ThinkingEnabled; got == nil || !*got {
		t.Fatal("finalJSON's own call should request thinking")
	}
}
