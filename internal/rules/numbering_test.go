package rules

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

func TestNumberingGapInArticles(t *testing.T) {
	// Pasal 3 was deleted, leaving 1, 2, 4.
	d := doc(t, `Pasal 1
Ketentuan pertama.

Pasal 2
Ketentuan kedua.

Pasal 4
Ketentuan keempat.`)

	fs := ValidateNumbering(d)
	gaps := findingsOf(fs, docmodel.CatNumberingGap)
	if len(gaps) != 1 {
		t.Fatalf("expected one gap finding, got %d: %+v", len(gaps), fs)
	}
	if !strings.Contains(gaps[0].Message, "Pasal 3") {
		t.Errorf("message should name the missing article, got: %s", gaps[0].Message)
	}
	if gaps[0].Class != docmodel.ClassVerified {
		t.Errorf("Class = %q, want verified", gaps[0].Class)
	}
}

func TestNumberingDuplicate(t *testing.T) {
	d := doc(t, `Pasal 1
Ketentuan pertama.

Pasal 2
Ketentuan kedua.

Pasal 2
Ketentuan kedua yang tidak sengaja diberi nomor sama.`)

	fs := ValidateNumbering(d)
	dups := findingsOf(fs, docmodel.CatNumberingDuplicate)
	if len(dups) != 1 {
		t.Fatalf("expected one duplicate finding, got %d: %+v", len(dups), fs)
	}
	if !strings.Contains(dups[0].Message, "dua kali") {
		t.Errorf("unexpected message: %s", dups[0].Message)
	}
}

func TestNumberingGapInAyat(t *testing.T) {
	// Ayat numbering restarts per Pasal, so the gap is scoped to Pasal 1 only.
	d := doc(t, `Pasal 1
(1) Ketentuan pertama.
(3) Ketentuan ketiga, ayat (2) hilang.

Pasal 2
(1) Ketentuan pertama pasal dua.
(2) Ketentuan kedua pasal dua.`)

	fs := ValidateNumbering(d)
	gaps := findingsOf(fs, docmodel.CatNumberingGap)
	if len(gaps) != 1 {
		t.Fatalf("expected exactly one gap (Pasal 2 restarts cleanly), got %d: %+v", len(gaps), fs)
	}
	if !strings.Contains(gaps[0].Message, "ayat") {
		t.Errorf("message should be about ayat, got: %s", gaps[0].Message)
	}
	if !strings.Contains(gaps[0].Message, "Pasal 1") {
		t.Errorf("message should scope the gap to Pasal 1, got: %s", gaps[0].Message)
	}
}

func TestNumberingRenumberActionIsGrounded(t *testing.T) {
	d := doc(t, `Pasal 1
(1) Ketentuan pertama.
(3) Ketentuan yang seharusnya bernomor dua.`)

	fs := ValidateNumbering(d)
	gaps := findingsOf(fs, docmodel.CatNumberingGap)
	if len(gaps) == 0 {
		t.Fatalf("expected a gap finding, got: %+v", fs)
	}
	if len(gaps[0].Actions) == 0 {
		t.Fatal("gap finding should propose a renumber action")
	}
	a := gaps[0].Actions[0]
	if a.Type != docmodel.ActionRenumber {
		t.Errorf("action type = %q, want renumber", a.Type)
	}
	if a.Old != "(3)" || a.New != "(2)" {
		t.Errorf("action = %q -> %q, want \"(3)\" -> \"(2)\"", a.Old, a.New)
	}
	// Grounding: the quoted marker must appear verbatim in the target paragraph.
	if a.ParagraphIndex == nil {
		t.Fatal("action must name a paragraph")
	}
	if !strings.Contains(d.Paragraphs[*a.ParagraphIndex].Text, a.Old) {
		t.Errorf("action not grounded: %q not in [%d] %q",
			a.Old, *a.ParagraphIndex, d.Paragraphs[*a.ParagraphIndex].Text)
	}
}

func TestNumberingBadStart(t *testing.T) {
	d := doc(t, `Pasal 1
(2) Ayat ini seharusnya bernomor satu.
(3) Ayat berikutnya.`)

	fs := ValidateNumbering(d)
	bad := findingsOf(fs, docmodel.CatNumberingBadStart)
	if len(bad) != 1 {
		t.Fatalf("expected one bad-start finding, got %d: %+v", len(bad), fs)
	}
	if len(bad[0].Actions) == 0 || bad[0].Actions[0].New != "(1)" {
		t.Errorf("expected a renumber to (1), got %+v", bad[0].Actions)
	}
}

func TestNumberingHurufSequence(t *testing.T) {
	d := doc(t, `Pasal 1
(1) Menteri berwenang:
a. menetapkan kebijakan;
c. melakukan pengawasan.`)

	fs := ValidateNumbering(d)
	gaps := findingsOf(fs, docmodel.CatNumberingGap)
	if len(gaps) != 1 {
		t.Fatalf("expected one gap for the missing huruf b, got %d: %+v", len(gaps), fs)
	}
	if !strings.Contains(gaps[0].Message, "huruf b") {
		t.Errorf("message should name huruf b, got: %s", gaps[0].Message)
	}
}

func TestNumberingCleanDocumentHasNoFindings(t *testing.T) {
	d := doc(t, `BAB I
KETENTUAN UMUM

Pasal 1
(1) Ketentuan pertama.
(2) Ketentuan kedua:
a. bagian pertama;
b. bagian kedua.

Pasal 2
(1) Ketentuan lain.`)

	if fs := ValidateNumbering(d); len(fs) != 0 {
		t.Errorf("expected no findings for a well-numbered document, got: %+v", fs)
	}
}

func TestDecimalClausePrefixMismatch(t *testing.T) {
	// Clauses under Article 2 must be numbered 2.x, not 3.x.
	d := doc(t, `Article 2
Term

2.1 This Agreement commences on the Effective Date.
3.2 The term shall be five years.`)

	fs := ValidateNumbering(d)
	var found bool
	for _, f := range fs {
		if strings.Contains(f.Message, "diawali") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a decimal-prefix mismatch finding, got: %+v", fs)
	}
}

func TestSingleItemListIsNotFlagged(t *testing.T) {
	d := doc(t, `Pasal 1
(1) Satu-satunya ayat dalam pasal ini.`)
	if fs := ValidateNumbering(d); len(fs) != 0 {
		t.Errorf("a one-item list should not be flagged, got: %+v", fs)
	}
}
