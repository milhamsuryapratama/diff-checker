package agentic

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/trace"
)

func fixture(t *testing.T, scenario, side string) string {
	t.Helper()
	// Tests run from the package directory; the fixtures live at the repo root.
	return filepath.Join("..", "..", "testdata", "scenarios", scenario, side+".docx")
}

// This runs the real compiled graph — fan-out, join, conditional edge and all —
// against real DOCX files, with the LLM tier switched off. It is the test that
// proves the graph is wired correctly, as distinct from its nodes being
// individually correct.
func TestGraphRunsEndToEndWithoutLLM(t *testing.T) {
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	opts := DefaultOptions()
	opts.NoLLM = true

	var progress []Progress
	rec := trace.NewBuffer(nil)
	res, err := p.Run(context.Background(),
		fixture(t, "kitchen_sink", "prev"),
		fixture(t, "kitchen_sink", "curr"),
		opts,
		func(pr Progress) { progress = append(progress, pr) },
		rec,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The deterministic findings must match what the CLI produces for this
	// fixture: one critical broken reference, three major numbering/structure
	// findings. If the graph path diverged from report.Build, this is where it
	// would show.
	if res.Report.Summary.Critical != 1 {
		t.Errorf("critical = %d, want 1", res.Report.Summary.Critical)
	}
	if res.Report.Summary.Major != 3 {
		t.Errorf("major = %d, want 3", res.Report.Summary.Major)
	}

	// No LLM tier means no advisory findings and, crucially, no spend.
	for _, f := range res.Report.Findings {
		if f.Class == docmodel.ClassAdvisory && f.Severity != docmodel.SeverityInfo {
			t.Errorf("advisory finding produced with LLM disabled: %+v", f)
		}
	}
	if got := res.Usage.Totals(); got.Calls != 0 || got.USD != 0 {
		t.Errorf("LLM disabled but usage recorded: %+v", got)
	}

	// Progress must cover the deterministic path so the UI checklist is real.
	seen := map[string]bool{}
	for _, pr := range progress {
		if pr.Status == "done" {
			seen[pr.Node] = true
		}
	}
	for _, node := range []string{NodeIngestPrev, NodeIngestCurr, NodeDeterm, NodeTriage, NodeAssemble} {
		if !seen[node] {
			t.Errorf("no completion event for node %q; got %v", node, seen)
		}
	}
	// The conditional edge must have routed around the paid tiers.
	if seen[NodeAnalyze] || seen[NodeRecommend] {
		t.Error("LLM nodes executed despite NoLLM")
	}

	// The deterministic steps narrate their own work, so the reasoning panel is
	// useful even when no model runs — that is the half of the record which is
	// actually reproducible.
	entries := rec.Entries()
	if len(entries) == 0 {
		t.Fatal("deterministic run recorded no reasoning")
	}
	var sawPlan bool
	tracedNodes := map[string]bool{}
	for _, e := range entries {
		tracedNodes[e.Node] = true
		if strings.Contains(e.Text, "Merencanakan penomoran") {
			sawPlan = true
		}
	}
	for _, node := range []string{NodeIngestPrev, NodeIngestCurr, NodeDeterm} {
		if !tracedNodes[node] {
			t.Errorf("node %q recorded nothing", node)
		}
	}
	if !sawPlan {
		t.Error("renumbering plan not explained in the trace")
	}
}

// The graph must fail loudly on an unreadable document rather than reporting a
// half-built comparison as complete.
func TestGraphFailsOnMissingDocument(t *testing.T) {
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opts := DefaultOptions()
	opts.NoLLM = true

	_, err = p.Run(context.Background(),
		fixture(t, "kitchen_sink", "prev"),
		filepath.Join("..", "..", "testdata", "tidak-ada.docx"),
		opts, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a missing document, got none")
	}
}

// A text-only revision must still traverse the graph and report cleanly.
func TestGraphOnTextOnlyFixture(t *testing.T) {
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opts := DefaultOptions()
	opts.NoLLM = true

	res, err := p.Run(context.Background(),
		fixture(t, "text_only", "prev"),
		fixture(t, "text_only", "curr"),
		opts, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Report.Summary.Critical != 0 || res.Report.Summary.Major != 0 {
		t.Errorf("text-only fixture should have no structural findings, got %+v", res.Report.Summary)
	}
	if res.Report.Summary.Modified != 3 {
		t.Errorf("modified = %d, want 3", res.Report.Summary.Modified)
	}
}
