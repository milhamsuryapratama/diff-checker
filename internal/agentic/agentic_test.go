package agentic

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/graph"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// The conditional edge after triage is the pipeline's main cost control, and
// it is a pure function of state, so it is worth pinning down exactly.
func TestRouteAfterTriage(t *testing.T) {
	tests := []struct {
		name  string
		state graph.State
		want  string
	}{
		{
			name:  "substantive changes go to analyze",
			state: graph.State{keyTriage: &TriageVerdict{Substantive: true, ChangeIDs: []int{1}}},
			want:  routeAnalyze,
		},
		{
			name:  "cosmetic-only skips straight to assemble",
			state: graph.State{keyTriage: &TriageVerdict{Substantive: false}},
			want:  routeAssemble,
		},
		{
			name: "substantive but no IDs still skips: nothing to analyse",
			state: graph.State{
				keyTriage: &TriageVerdict{Substantive: true, ChangeIDs: nil},
			},
			want: routeAssemble,
		},
		{
			name: "no-llm mode never reaches the paid tiers",
			state: graph.State{
				keyTriage:  &TriageVerdict{Substantive: true, ChangeIDs: []int{1}},
				keyOptions: Options{NoLLM: true},
			},
			want: routeAssemble,
		},
		{
			name:  "missing triage fails closed",
			state: graph.State{},
			want:  routeAssemble,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := routeAfterTriage(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("routeAfterTriage: %v", err)
			}
			if got != tc.want {
				t.Errorf("route = %q, want %q", got, tc.want)
			}
		})
	}
}

// A hallucinated change ID must not be able to pull an unrelated change into
// the expensive tier.
func TestIntersectIDsDropsUnknown(t *testing.T) {
	offered := []docmodel.Change{{ID: 10}, {ID: 20}}

	got := intersectIDs([]int{10, 999, 20}, offered)

	if len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Errorf("intersectIDs = %v, want [10 20]", got)
	}
}

// Classification is grounded too: an ID or taxonomy value outside what was
// offered is dropped rather than coerced to the nearest match.
func TestKeepValidAnalyses(t *testing.T) {
	offered := []docmodel.Change{{ID: 1}, {ID: 2}}

	got := keepValidAnalyses([]AnalyzedChange{
		{ChangeID: 1, Kind: KindTermExtension, Severity: "major"},
		{ChangeID: 2, Kind: "invented_category", Severity: "critical"},
		{ChangeID: 99, Kind: KindTypo, Severity: "info"},
	}, offered)

	if len(got) != 1 {
		t.Fatalf("expected only the valid entry to survive, got %d: %+v", len(got), got)
	}
	if got[0].ChangeID != 1 {
		t.Errorf("wrong entry kept: %+v", got[0])
	}
}

// An unrecognised severity must never be able to present itself as critical.
func TestNormalizeSeverity(t *testing.T) {
	tests := map[string]string{
		"critical":  "critical",
		"CRITICAL":  "critical",
		"kritis":    "critical",
		"major":     "major",
		"mayor":     "major",
		"info":      "info",
		"minor":     "minor",
		"":          "minor",
		"KATASTROF": "minor",
	}
	for in, want := range tests {
		if got := normalizeSeverity(in); got != want {
			t.Errorf("normalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstantiveChangesFiltersAndCaps(t *testing.T) {
	changes := []docmodel.Change{
		{ID: 1},
		{ID: 2, Cosmetic: true},
		{ID: 3},
		{ID: 4},
	}

	got := substantiveChanges(changes, 0)
	if len(got) != 3 {
		t.Fatalf("cosmetic change not filtered: %+v", got)
	}

	capped := substantiveChanges(changes, 2)
	if len(capped) != 2 {
		t.Errorf("limit not applied: got %d, want 2", len(capped))
	}
}

func TestRiskyAnalysesDropsCosmeticAndInfo(t *testing.T) {
	got := riskyAnalyses([]AnalyzedChange{
		{ChangeID: 1, Kind: KindTypo, Severity: "minor"},
		{ChangeID: 2, Kind: KindFormatting, Severity: "major"},
		{ChangeID: 3, Kind: KindLiabilityShift, Severity: "info"},
		{ChangeID: 4, Kind: KindTermExtension, Severity: "major"},
	})

	if len(got) != 1 || got[0].ChangeID != 4 {
		t.Errorf("riskyAnalyses = %+v, want only change 4", got)
	}
}

// Structured output is not honoured strictly by every provider, so the decoder
// has to survive fences and surrounding prose without ever guessing.
func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Substantive bool `json:"substantive"`
	}

	tests := []struct {
		name    string
		in      string
		want    bool
		wantErr bool
	}{
		{name: "bare object", in: `{"substantive":true}`, want: true},
		{name: "fenced", in: "```json\n{\"substantive\":true}\n```", want: true},
		{name: "fenced without language", in: "```\n{\"substantive\":true}\n```", want: true},
		{
			name: "embedded in prose",
			in:   `Berikut hasilnya: {"substantive":true} — selesai.`,
			want: true,
		},
		{
			name: "brace inside a string literal does not end the object",
			in:   `{"substantive":true,"reason":"lihat {ini}"}`,
			want: true,
		},
		{name: "empty", in: "   ", wantErr: true},
		{name: "not json at all", in: "maaf, saya tidak bisa", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out payload
			err := decodeJSON(tc.in, &out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeJSON: %v", err)
			}
			if out.Substantive != tc.want {
				t.Errorf("Substantive = %v, want %v", out.Substantive, tc.want)
			}
		})
	}
}

// Usage is written by several nodes, some of them concurrently, so the reducer
// must keep the one accumulator rather than overwrite it.
func TestUsageAccumulates(t *testing.T) {
	u := &Usage{}
	u.Add("analyze", NodeUsage{Model: "m", Input: 100, Output: 50, USD: 0.1})
	u.Add("analyze", NodeUsage{Model: "m", Input: 200, Output: 20, USD: 0.2})
	u.Add("triage", NodeUsage{Model: "s", Input: 10, Output: 5, USD: 0.01})

	nodes := u.Nodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	// Sorted by name: analyze before triage.
	if nodes[0].Node != "analyze" || nodes[0].Calls != 2 || nodes[0].Input != 300 {
		t.Errorf("analyze usage wrong: %+v", nodes[0])
	}

	tot := u.Totals()
	if tot.Calls != 3 || tot.Input != 310 || tot.Output != 75 {
		t.Errorf("totals wrong: %+v", tot)
	}
	if got := tot.USD; got < 0.30 || got > 0.32 {
		t.Errorf("total USD = %v, want ~0.31", got)
	}
}

func TestMergeUsageKeepsExistingAccumulator(t *testing.T) {
	existing := &Usage{}
	existing.Add("n", NodeUsage{Input: 5})

	merged := mergeUsage(existing, &Usage{})

	if merged != any(existing) {
		t.Fatal("reducer replaced the shared accumulator instead of keeping it")
	}
	if merged.(*Usage).Totals().Input != 5 {
		t.Error("accumulated usage lost during merge")
	}
}

// Cached input must be billed at the cache-read rate and excluded from the
// ordinary input count, or the cost report double-charges it.
func TestCostSeparatesCachedInput(t *testing.T) {
	// Sonnet-shaped rates: cache reads a tenth of input.
	r := models.Rates{InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75}

	full := r.Cost(1_000_000, 0, 0, 0)
	cached := r.Cost(0, 1_000_000, 0, 0)

	if full <= cached {
		t.Errorf("cached input should be cheaper: full=%v cached=%v", full, cached)
	}
	if got, want := cached, full*0.1; got < want*0.99 || got > want*1.01 {
		t.Errorf("cache read = %v, want ~%v (a tenth of input)", got, want)
	}
}

func TestChangeKindCosmetic(t *testing.T) {
	if !KindTypo.Cosmetic() || !KindFormatting.Cosmetic() {
		t.Error("typo and formatting must count as cosmetic")
	}
	if KindLiabilityShift.Cosmetic() || KindTermExtension.Cosmetic() {
		t.Error("substantive kinds must not count as cosmetic")
	}
	if KindTypo.Valid() != true || ChangeKind("nonsense").Valid() {
		t.Error("Valid() does not match the closed taxonomy")
	}
}

// buildAdvisory must never emit a ClassVerified finding: the class split is the
// product's trust boundary.
func TestBuildAdvisoryAlwaysAdvisory(t *testing.T) {
	changes := []docmodel.Change{{ID: 1, CurrIndexes: []int{7}, NodeID: "pasal:2"}}

	out := buildAdvisory(
		&AnalysisResult{Changes: []AnalyzedChange{
			{ChangeID: 1, Kind: KindTermExtension, Severity: "major", Summary: "jangka waktu berubah"},
		}},
		&RecommendationResult{Recommendations: []Recommendation{
			{ChangeID: 1, Severity: "critical", Risk: "risiko", Recommendation: "perbaiki"},
		}},
		changes,
	)

	if len(out) != 1 {
		t.Fatalf("a change covered by a recommendation should yield one finding, got %d", len(out))
	}
	for _, f := range out {
		if f.Class != docmodel.ClassAdvisory {
			t.Errorf("finding %s is %s, must be advisory", f.ID, f.Class)
		}
	}
	if out[0].ParaIndex == nil || *out[0].ParaIndex != 7 {
		t.Errorf("finding not located at the change's paragraph: %+v", out[0].ParaIndex)
	}
}

func TestBuildAdvisoryReportsUncoveredChanges(t *testing.T) {
	changes := []docmodel.Change{{ID: 1}, {ID: 2}}

	out := buildAdvisory(
		&AnalysisResult{Changes: []AnalyzedChange{
			{ChangeID: 1, Kind: KindTermExtension, Severity: "major", Summary: "a"},
			{ChangeID: 2, Kind: KindDefinitionChange, Severity: "minor", Summary: "b"},
		}},
		&RecommendationResult{Recommendations: []Recommendation{
			{ChangeID: 1, Severity: "major", Risk: "risiko"},
		}},
		changes,
	)

	if len(out) != 2 {
		t.Fatalf("expected the uncovered change to still be reported, got %d", len(out))
	}
}

// The prompt block handed to the model must state the verified findings as
// facts, so the model does not spend tokens rediscovering or contradicting them.
func TestVerifiedFactsBlockStatesFindingsAsFacts(t *testing.T) {
	rep := &docmodel.Report{PrevArticleCount: 10, CurrArticleCount: 9}
	rep.AddFinding(docmodel.Finding{
		Class: docmodel.ClassVerified, Category: docmodel.CatBrokenReference,
		Severity: docmodel.SeverityCritical, Message: "Pasal 3 dihapus tapi masih dirujuk",
	})

	got := verifiedFactsBlock(rep)

	for _, want := range []string{"JANGAN dibantah", "10 -> 9", "Pasal 3 dihapus tapi masih dirujuk"} {
		if !strings.Contains(got, want) {
			t.Errorf("facts block missing %q:\n%s", want, got)
		}
	}
}
