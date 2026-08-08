package rules

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

func build(t *testing.T, lines ...string) *docmodel.IndexedDoc {
	t.Helper()
	doc := ingest.ParseTextBytes("t.txt", []byte(strings.Join(lines, "\n")))
	structure.Build(doc)
	return doc
}

// The defect this whole file exists for: individually reasonable repairs that
// contradict each other. A document numbered 2, 2, 4, 8, 6 drew "2→1", "8→5"
// and "6→9" from three separate checks, which applied together leave
// 1, 2, 4, 5, 9 — still gapped and still out of order.
func TestPlanIsGloballyConsistent(t *testing.T) {
	doc := build(t,
		"PASAL 2", "Isi satu.",
		"PASAL 2", "Isi dua.",
		"PASAL 4", "Isi tiga.",
		"PASAL 8", "Isi empat.",
		"PASAL 6", "Isi lima.",
	)

	plan := PlanRenumbering(doc)

	if got := plan.Verify(doc); len(got) > 0 {
		t.Errorf("plan leaves defects behind: %v", got)
	}
	want := []string{"1", "2", "3", "4", "5"}
	seq := plan.Sequences[0]
	for i, w := range want {
		if seq.After[i] != w {
			t.Errorf("final sequence = %v, want %v", seq.After, want)
			break
		}
	}
}

// A correctly numbered document must draw no plan at all.
func TestPlanIsEmptyWhenAlreadyOrdered(t *testing.T) {
	doc := build(t, "PASAL 1", "Isi satu.", "PASAL 2", "Isi dua.", "PASAL 3", "Isi tiga.")

	plan := PlanRenumbering(doc)

	if len(plan.ByParagraph) != 0 || len(plan.Sequences) != 0 {
		t.Errorf("ordered document produced a plan: %+v", plan.Sequences)
	}
}

// Each language of a multilingual document numbers from 1 independently, so
// each is its own sequence. Treating them as one reads three correct runs as a
// single run full of duplicates.
func TestEachVocabularyIsItsOwnSequence(t *testing.T) {
	doc := build(t,
		"ARTIKEL 1", "FASAL 1", "ARTICLE 1", "Isi.",
		"ARTIKEL 2", "FASAL 2", "ARTICLE 2", "Isi.",
		"ARTIKEL 5", "FASAL 3", "ARTICLE 3", "Isi.",
	)

	plan := PlanRenumbering(doc)

	if len(plan.Sequences) != 1 {
		t.Fatalf("expected only the broken run to need fixing, got %d: %+v",
			len(plan.Sequences), plan.Sequences)
	}
	if !strings.Contains(strings.ToLower(plan.Sequences[0].Label), "artikel") {
		t.Errorf("wrong run planned: %q", plan.Sequences[0].Label)
	}
	if got := plan.Verify(doc); len(got) > 0 {
		t.Errorf("plan leaves defects behind: %v", got)
	}
}

// One action per paragraph, or the plan cannot be applied without ordering
// rules the caller does not have.
func TestPlanHasOneActionPerParagraph(t *testing.T) {
	doc := build(t, "PASAL 5", "Isi satu.", "PASAL 9", "Isi dua.", "PASAL 2", "Isi tiga.")

	plan := PlanRenumbering(doc)

	for idx, act := range plan.ByParagraph {
		if act.ParagraphIndex == nil || *act.ParagraphIndex != idx {
			t.Errorf("action at %d points elsewhere: %+v", idx, act)
		}
	}
}
