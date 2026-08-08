package textdiff

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

func TestCompareIdenticalDocuments(t *testing.T) {
	const text = "Pasal 1\n(1) Ketentuan pertama.\n(2) Ketentuan kedua."
	if got := Compare(doc(t, text), doc(t, text)); len(got) != 0 {
		t.Errorf("identical documents produced %d changes: %+v", len(got), got)
	}
}

func TestCompareModified(t *testing.T) {
	prev := doc(t, "Pasal 1\n(1) Jangka waktu 30 (tiga puluh) hari kerja.")
	curr := doc(t, "Pasal 1\n(1) Jangka waktu 60 (enam puluh) hari kerja.")

	changes := Compare(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Type != docmodel.ChangeModified {
		t.Errorf("Type = %q, want modified", c.Type)
	}
	if c.Cosmetic {
		t.Error("a term change must not be classified as cosmetic")
	}
	// The highlight must mark the numbers that actually changed and leave the
	// surrounding wording alone.
	if !strings.Contains(c.PrevHTML, "<b>") || !strings.Contains(c.CurrHTML, "<b>") {
		t.Errorf("expected highlights\nprev: %s\ncurr: %s", c.PrevHTML, c.CurrHTML)
	}
	// Whole tokens must be highlighted, not fragments of them: a reviewer should
	// see "<b>60</b>", never "<b>6</b>0".
	if !strings.Contains(c.CurrHTML, "<b>60</b>") {
		t.Errorf("current side should highlight the whole new value: %s", c.CurrHTML)
	}
	if !strings.Contains(c.PrevHTML, "<b>30</b>") {
		t.Errorf("previous side should highlight the whole old value: %s", c.PrevHTML)
	}
	if !strings.Contains(c.PrevHTML, "Jangka waktu") {
		t.Errorf("unchanged words should survive unhighlighted: %s", c.PrevHTML)
	}
	if c.Context != "Pasal 1 ayat (1)" {
		t.Errorf("Context = %q, want \"Pasal 1 ayat (1)\"", c.Context)
	}
}

func TestCompareAddedAndRemoved(t *testing.T) {
	prev := doc(t, "Pasal 1\nKetentuan pertama.\n\nPasal 2\nKetentuan kedua.")
	curr := doc(t, "Pasal 1\nKetentuan pertama.")

	changes := Compare(prev, curr)
	if len(changes) == 0 {
		t.Fatal("expected at least one change")
	}
	var sawRemoved bool
	for _, c := range changes {
		if c.Type == docmodel.ChangeRemoved && strings.Contains(c.PrevText, "Pasal 2") {
			sawRemoved = true
		}
	}
	if !sawRemoved {
		t.Errorf("expected a removal covering Pasal 2, got: %+v", changes)
	}

	// And the reverse direction reports an addition.
	changes = Compare(curr, prev)
	var sawAdded bool
	for _, c := range changes {
		if c.Type == docmodel.ChangeAdded && strings.Contains(c.CurrText, "Pasal 2") {
			sawAdded = true
		}
	}
	if !sawAdded {
		t.Errorf("expected an addition covering Pasal 2, got: %+v", changes)
	}
}

func TestCompareCosmeticDetection(t *testing.T) {
	prev := doc(t, "Pasal 1\nKetentuan ini berlaku, sejak tanggal ditetapkan.")
	curr := doc(t, "Pasal 1\nKetentuan ini berlaku sejak tanggal ditetapkan.")

	changes := Compare(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if !changes[0].Cosmetic {
		t.Errorf("a comma-only change should be cosmetic: %q -> %q",
			changes[0].PrevText, changes[0].CurrText)
	}
	if got := len(Substantive(changes)); got != 0 {
		t.Errorf("Substantive() returned %d changes, want 0", got)
	}
}

// TestCaseChangeIsNotCosmetic pins a deliberate decision: in a legal document a
// defined term ("Pihak Pertama") and a common noun ("pihak pertama") are not the
// same thing, so a case change must reach the review tier.
func TestCaseChangeIsNotCosmetic(t *testing.T) {
	prev := doc(t, "Pasal 1\nKewajiban Pihak Pertama diatur tersendiri.")
	curr := doc(t, "Pasal 1\nKewajiban pihak pertama diatur tersendiri.")

	changes := Compare(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Cosmetic {
		t.Error("case change was classified as cosmetic")
	}
}

func TestHighlightEscapesHTML(t *testing.T) {
	prev := doc(t, "Pasal 1\nNilai kontrak <500 juta rupiah.")
	curr := doc(t, "Pasal 1\nNilai kontrak <900 juta rupiah.")

	changes := Compare(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	// Document content must never inject markup into the report. The "<" is an
	// unchanged token so it renders outside the highlight, but it must still be
	// escaped.
	if strings.Contains(changes[0].PrevHTML, "<500") {
		t.Errorf("raw angle bracket leaked into output: %s", changes[0].PrevHTML)
	}
	if !strings.Contains(changes[0].PrevHTML, "&lt;") {
		t.Errorf("expected the angle bracket to be escaped, got: %s", changes[0].PrevHTML)
	}
	// Only <b> tags may appear as live markup.
	stripped := strings.NewReplacer("<b>", "", "</b>", "").Replace(changes[0].PrevHTML)
	if strings.ContainsAny(stripped, "<>") {
		t.Errorf("unescaped markup survived outside <b> tags: %s", changes[0].PrevHTML)
	}
}

func TestCompareIndexesArePreserved(t *testing.T) {
	prev := doc(t, "Baris satu.\nBaris dua.\nBaris tiga.")
	curr := doc(t, "Baris satu.\nBaris dua diubah.\nBaris tiga.")

	changes := Compare(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	c := changes[0]
	if len(c.PrevIndexes) != 1 || c.PrevIndexes[0] != 1 {
		t.Errorf("PrevIndexes = %v, want [1]", c.PrevIndexes)
	}
	if len(c.CurrIndexes) != 1 || c.CurrIndexes[0] != 1 {
		t.Errorf("CurrIndexes = %v, want [1]", c.CurrIndexes)
	}
	// Indexes must slice back to the exact paragraph they claim.
	if got := prev.Paragraphs[c.PrevIndexes[0]].Text; got != "Baris dua." {
		t.Errorf("PrevIndexes points at %q, want \"Baris dua.\"", got)
	}
}

func TestSummarize(t *testing.T) {
	changes := []docmodel.Change{
		{Type: docmodel.ChangeAdded},
		{Type: docmodel.ChangeAdded},
		{Type: docmodel.ChangeRemoved},
		{Type: docmodel.ChangeModified},
	}
	s := Summarize(changes)
	if s.Added != 2 || s.Removed != 1 || s.Modified != 1 || s.Total != 4 {
		t.Errorf("Summarize = %+v", s)
	}
}

func TestChangeIDsAreStable(t *testing.T) {
	prev := doc(t, "Satu.\nDua.\nTiga.")
	curr := doc(t, "Satu diubah.\nDua.\nTiga diubah.")
	changes := Compare(prev, curr)
	if len(changes) < 2 {
		t.Fatalf("expected at least 2 changes, got %d", len(changes))
	}
	// IDs follow the (n+1)*10 convention inherited from mining-legal-backend, so
	// existing tooling and saved reports keep the same addressing.
	for i, c := range changes {
		if want := (i + 1) * 10; c.ID != want {
			t.Errorf("change %d has ID %d, want %d", i, c.ID, want)
		}
	}
}
