package rules

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

func doc(t *testing.T, text string) *docmodel.IndexedDoc {
	t.Helper()
	d := ingest.ParseTextBytes("t.txt", []byte(text))
	structure.Build(d)
	return d
}

func findingsOf(fs []docmodel.Finding, cat docmodel.Category) []docmodel.Finding {
	var out []docmodel.Finding
	for _, f := range fs {
		if f.Category == cat {
			out = append(out, f)
		}
	}
	return out
}

// TestDeletedTargetBreaksReference is the scenario the design is built around:
// a paragraph cites Pasal 1.1, the revision deletes Pasal 1.1, and the engine
// must report which paragraph is now dangling — with no LLM involved.
func TestDeletedTargetBreaksReference(t *testing.T) {
	prev := doc(t, `Pasal 1.1
Definisi istilah dalam perjanjian ini ditetapkan oleh para pihak secara tertulis.

Pasal 1.2
Ruang lingkup perjanjian meliputi seluruh kegiatan operasional.

Pasal 2
Kewajiban para pihak tunduk pada Pasal 1.1 sebagaimana disebutkan di atas.`)

	// Pasal 1.1 is removed outright; its content does not reappear anywhere.
	curr := doc(t, `Pasal 1.2
Ruang lingkup perjanjian meliputi seluruh kegiatan operasional.

Pasal 2
Kewajiban para pihak tunduk pada Pasal 1.1 sebagaimana disebutkan di atas.`)

	fs := CompareReferences(prev, curr)

	broken := findingsOf(fs, docmodel.CatBrokenReference)
	if len(broken) == 0 {
		t.Fatalf("expected a broken-reference finding, got: %+v", fs)
	}

	f := broken[0]
	if f.Class != docmodel.ClassVerified {
		t.Errorf("Class = %q, want verified — this finding must never depend on a model", f.Class)
	}
	if f.Severity != docmodel.SeverityCritical {
		t.Errorf("Severity = %q, want critical: the revision under review introduced this", f.Severity)
	}
	if f.ParaIndex == nil {
		t.Fatal("finding must name the paragraph that now dangles")
	}
	if !strings.Contains(curr.Paragraphs[*f.ParaIndex].Text, "Pasal 1.1") {
		t.Errorf("finding points at [%d] = %q, which does not cite Pasal 1.1",
			*f.ParaIndex, curr.Paragraphs[*f.ParaIndex].Text)
	}
	if !strings.Contains(f.Message, "Pasal 1.1") {
		t.Errorf("message should name the missing target, got: %s", f.Message)
	}
	// With no relocation candidate the engine must not invent a repoint.
	for _, a := range f.Actions {
		if a.Type == docmodel.ActionReferenceUpdate {
			t.Errorf("proposed a repoint with no relocation evidence: %+v", a)
		}
	}
	if len(f.Actions) == 0 || f.Actions[0].Type != docmodel.ActionManualReview {
		t.Errorf("expected a manual-review action, got %+v", f.Actions)
	}
}

// TestRelocatedTargetProposesRepoint covers the other half of the same
// scenario: the article was renumbered rather than deleted, so the correct fix
// is to repoint the citation, not to remove it.
func TestRelocatedTargetProposesRepoint(t *testing.T) {
	prev := doc(t, `Pasal 1.1
Pembayaran dilakukan dalam jangka waktu tiga puluh hari kerja sejak tagihan diterima secara lengkap.

Pasal 2
Ketentuan pembayaran mengacu pada Pasal 1.1 perjanjian ini.`)

	// Same clause, renumbered to 1.2.
	curr := doc(t, `Pasal 1.2
Pembayaran dilakukan dalam jangka waktu tiga puluh hari kerja sejak tagihan diterima secara lengkap.

Pasal 2
Ketentuan pembayaran mengacu pada Pasal 1.1 perjanjian ini.`)

	fs := CompareReferences(prev, curr)
	shifted := findingsOf(fs, docmodel.CatShiftedReference)
	if len(shifted) == 0 {
		t.Fatalf("expected a shifted-reference finding, got: %+v", fs)
	}

	f := shifted[0]
	if !strings.Contains(f.Message, "1.2") {
		t.Errorf("message should name the relocation target, got: %s", f.Message)
	}
	var repoint *docmodel.Action
	for i := range f.Actions {
		if f.Actions[i].Type == docmodel.ActionReferenceUpdate {
			repoint = &f.Actions[i]
		}
	}
	if repoint == nil {
		t.Fatalf("expected a reference_update action, got %+v", f.Actions)
	}
	if repoint.Old != "Pasal 1.1" || repoint.New != "Pasal 1.2" {
		t.Errorf("repoint = %q -> %q, want \"Pasal 1.1\" -> \"Pasal 1.2\"", repoint.Old, repoint.New)
	}
	// The action must be grounded: Old has to appear verbatim in the target
	// paragraph, or applying it would silently do nothing.
	p := curr.Paragraphs[*repoint.ParagraphIndex]
	if !strings.Contains(p.Text, repoint.Old) {
		t.Errorf("action is not grounded: %q not found in [%d] %q",
			repoint.Old, *repoint.ParagraphIndex, p.Text)
	}
}

// TestPreexistingBrokenReferenceIsDeprioritised checks that a citation which was
// already dangling before the revision is not reported as a regression.
func TestPreexistingBrokenReferenceIsDeprioritised(t *testing.T) {
	prev := doc(t, `Pasal 1
Ketentuan ini mengacu pada Pasal 99 yang tidak pernah ada.`)
	curr := doc(t, `Pasal 1
Ketentuan ini mengacu pada Pasal 99 yang tidak pernah ada.`)

	fs := CompareReferences(prev, curr)
	broken := findingsOf(fs, docmodel.CatBrokenReference)
	if len(broken) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %+v", len(broken), broken)
	}
	if broken[0].Severity != docmodel.SeverityMinor {
		t.Errorf("Severity = %q, want minor for a pre-existing defect", broken[0].Severity)
	}
	if !strings.Contains(broken[0].Message, "sebelumnya") {
		t.Errorf("message should say the defect predates this revision, got: %s", broken[0].Message)
	}
}

func TestValidReferencesProduceNoFindings(t *testing.T) {
	d := doc(t, `Pasal 1
(1) Ketentuan umum berlaku bagi seluruh pihak.
(2) Sebagaimana dimaksud pada ayat (1), para pihak wajib tunduk.

Pasal 2
Ketentuan dalam Pasal 1 ayat (2) berlaku secara penuh.`)

	if fs := CheckReferences(d); len(fs) != 0 {
		t.Errorf("expected no findings for a self-consistent document, got: %+v", fs)
	}
}

func TestRemovedArticleReportedEvenIfUncited(t *testing.T) {
	prev := doc(t, `Pasal 1
Ketentuan pertama tentang kewajiban pelaporan berkala setiap tahun.

Pasal 2
Ketentuan kedua tentang sanksi administratif bagi pelanggar.`)
	curr := doc(t, `Pasal 1
Ketentuan pertama tentang kewajiban pelaporan berkala setiap tahun.`)

	fs := CompareReferences(prev, curr)
	removed := findingsOf(fs, docmodel.CatSectionRemoved)
	if len(removed) != 1 {
		t.Fatalf("expected one section_removed finding, got %d: %+v", len(removed), fs)
	}
	if !strings.Contains(removed[0].Message, "Pasal 2") {
		t.Errorf("message should name the removed article, got: %s", removed[0].Message)
	}
}

func TestSimilarity(t *testing.T) {
	a := "Pembayaran dilakukan dalam jangka waktu tiga puluh hari kerja"
	if got := similarity(a, a); got != 1 {
		t.Errorf("similarity of identical text = %v, want 1", got)
	}
	if got := similarity(a, "Ketentuan lain yang sama sekali berbeda isinya"); got > 0.2 {
		t.Errorf("similarity of unrelated text = %v, want low", got)
	}
	if got := similarity("", a); got != 0 {
		t.Errorf("similarity with empty = %v, want 0", got)
	}
}
