package structure

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
)

// sample mirrors the shape of a real Indonesian mining regulation: chapters with
// all-caps captions, articles, an "angka" definition list directly under Pasal 1,
// and an "ayat -> huruf" nesting under Pasal 2.
const sample = `BAB I
KETENTUAN UMUM

Pasal 1
Dalam Peraturan Menteri ini yang dimaksud dengan:
1. Pertambangan adalah sebagian atau seluruh tahapan kegiatan.
2. Mineral adalah senyawa anorganik yang terbentuk di alam.

Pasal 2
(1) Pertambangan dikelola berdasarkan asas manfaat dan keberlanjutan.
(2) Dalam melaksanakan ketentuan sebagaimana dimaksud pada ayat (1), Menteri berwenang:
a. menetapkan kebijakan nasional;
b. melakukan pembinaan dan pengawasan.
(3) Ketentuan lebih lanjut diatur dengan Peraturan Menteri.

BAB II
PERIZINAN BERUSAHA

Pasal 3
Setiap kegiatan sebagaimana dimaksud dalam Pasal 2 ayat (1) wajib memiliki izin.`

func buildSample(t *testing.T, text string) *docmodel.IndexedDoc {
	t.Helper()
	doc := ingest.ParseTextBytes("sample.txt", []byte(text))
	Build(doc)
	return doc
}

func TestBuildTree(t *testing.T) {
	doc := buildSample(t, sample)

	for _, id := range []string{
		"bab:I", "bab:II",
		"pasal:1", "pasal:2", "pasal:3",
		"pasal:1/angka:1", "pasal:1/angka:2",
		"pasal:2/ayat:1", "pasal:2/ayat:2", "pasal:2/ayat:3",
		"pasal:2/ayat:2/huruf:a", "pasal:2/ayat:2/huruf:b",
	} {
		if _, ok := doc.Node(id); !ok {
			t.Errorf("missing node %q", id)
		}
	}

	// The all-caps line under a BAB heading becomes its title.
	if bab, ok := doc.Node("bab:I"); !ok || bab.Title != "KETENTUAN UMUM" {
		t.Errorf("bab:I title = %q, want KETENTUAN UMUM", bab.Title)
	}

	// A definition list numbered "1., 2." sits directly under the Pasal even
	// though UU 12/2011 names that level "angka".
	if n, ok := doc.Node("pasal:1/angka:1"); ok {
		if n.Parent == nil || n.Parent.ID != "pasal:1" {
			t.Errorf("angka:1 parent = %v, want pasal:1", n.Parent)
		}
	}

	// "(3)" must close the huruf list and resume the ayat list, not nest inside
	// huruf b.
	if n, ok := doc.Node("pasal:2/ayat:3"); ok {
		if n.Parent == nil || n.Parent.ID != "pasal:2" {
			t.Errorf("ayat:3 parent = %v, want pasal:2", n.Parent.ID)
		}
	}

	if got, want := ArticleCount(doc), 3; got != want {
		t.Errorf("ArticleCount = %d, want %d", got, want)
	}
}

func TestBuildNodePath(t *testing.T) {
	doc := buildSample(t, sample)
	n, ok := doc.Node("pasal:2/ayat:2/huruf:a")
	if !ok {
		t.Fatal("node not found")
	}
	if got, want := n.Path(), "Pasal 2 ayat (2) huruf a"; got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestBuildRanges(t *testing.T) {
	doc := buildSample(t, sample)

	pasal1, _ := doc.Node("pasal:1")
	pasal2, _ := doc.Node("pasal:2")
	if pasal1.End >= pasal2.Start {
		t.Errorf("pasal:1 range [%d,%d] overlaps pasal:2 start %d",
			pasal1.Start, pasal1.End, pasal2.Start)
	}
	// Every paragraph inside Pasal 1's range must actually resolve to it.
	for i := pasal1.Start; i <= pasal1.End; i++ {
		owner := OwnerOf(doc, i)
		if owner == nil {
			t.Fatalf("paragraph %d has no owner", i)
		}
		found := false
		for cur := owner; cur != nil; cur = cur.Parent {
			if cur.ID == "pasal:1" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("paragraph %d owner %q is not under pasal:1", i, owner.ID)
		}
	}
}

func TestBuildReferences(t *testing.T) {
	doc := buildSample(t, sample)

	var absolute, relative int
	for _, r := range doc.References {
		switch r.Kind {
		case docmodel.RefAbsolute:
			absolute++
		case docmodel.RefRelative:
			relative++
		}
	}
	if absolute == 0 {
		t.Fatalf("no absolute references found: %+v", doc.References)
	}

	// Pasal 3 cites "Pasal 2 ayat (1)".
	var found bool
	for _, r := range doc.References {
		if r.Pasal == "2" && r.Ayat == "1" && r.Kind == docmodel.RefAbsolute {
			found = true
			if got := r.TargetID(); got != "pasal:2/ayat:1" {
				t.Errorf("TargetID = %q, want pasal:2/ayat:1", got)
			}
		}
	}
	if !found {
		t.Errorf("reference to Pasal 2 ayat (1) not found: %+v", doc.References)
	}

	// The relative "pada ayat (1)" inside Pasal 2 must resolve to Pasal 2.
	var rel *docmodel.Reference
	for i := range doc.References {
		if doc.References[i].Kind == docmodel.RefRelative {
			rel = &doc.References[i]
			break
		}
	}
	if rel == nil {
		t.Fatal("expected a relative reference")
	}
	if rel.Pasal != "2" {
		t.Errorf("relative reference resolved to Pasal %q, want 2", rel.Pasal)
	}
	if got := rel.TargetID(); got != "pasal:2/ayat:1" {
		t.Errorf("relative TargetID = %q, want pasal:2/ayat:1", got)
	}
}

// TestBuildHeadingIsNotSelfReference guards the case where "Pasal 12" as a
// heading would otherwise be recorded as a reference to itself.
func TestBuildHeadingIsNotSelfReference(t *testing.T) {
	doc := buildSample(t, sample)
	for _, r := range doc.References {
		p := doc.Paragraphs[r.ParaIndex]
		if h, ok := ParseHeading(p.Text); ok && r.Start < h.PrefixLen {
			t.Errorf("heading %q at [%d] recorded as a reference to itself", p.Text, r.ParaIndex)
		}
	}
}

func TestBuildDuplicateHeadingsBothSurvive(t *testing.T) {
	doc := buildSample(t, "Pasal 5\nIsi pertama.\n\nPasal 5\nIsi kedua.")
	if _, ok := doc.Node("pasal:5"); !ok {
		t.Error("first Pasal 5 missing")
	}
	if _, ok := doc.Node("pasal:5#2"); !ok {
		t.Error("duplicate Pasal 5 was overwritten instead of kept for the validator")
	}
}

func TestBuildContractStyle(t *testing.T) {
	const contract = `Article 1
Definitions

1.1 In this Agreement, the following terms shall have the meanings set out below.
1.2 References to Clause 1.1 shall include any amendment thereto.

Article 2
Term

2.1 This Agreement shall commence on the Effective Date.`

	doc := buildSample(t, contract)
	if doc.Lang != "en" {
		t.Errorf("Lang = %q, want en", doc.Lang)
	}
	for _, id := range []string{"article:1", "article:2", "article:1/clause:1.1", "article:1/clause:1.2"} {
		if _, ok := doc.Node(id); !ok {
			t.Errorf("missing node %q", id)
		}
	}
	// "Clause 1.1" inside 1.2 must be captured as a reference.
	var found bool
	for _, r := range doc.References {
		if r.Pasal == "1.1" {
			found = true
		}
	}
	if !found {
		t.Errorf("reference to Clause 1.1 not found: %+v", doc.References)
	}
}

func TestParseHeading(t *testing.T) {
	tests := []struct {
		in      string
		wantOK  bool
		kind    docmodel.NodeKind
		number  string
		ordinal int
	}{
		{"BAB I", true, docmodel.KindBab, "I", 1},
		{"BAB XII", true, docmodel.KindBab, "XII", 12},
		{"Bagian Kesatu", true, docmodel.KindBagian, "Kesatu", 1},
		{"Bagian Ketiga", true, docmodel.KindBagian, "Ketiga", 3},
		{"Paragraf 2", true, docmodel.KindParagraf, "2", 2},
		{"Pasal 12", true, docmodel.KindPasal, "12", 12},
		{"Pasal 1.1", true, docmodel.KindPasal, "1.1", 1},
		{"Article 5", true, docmodel.KindArticle, "5", 5},
		{"Clause 3.2", true, docmodel.KindArticle, "3.2", 2},

		// Prose that merely contains the keyword is not a heading.
		{"dalam Pasal 12 disebutkan bahwa", false, 0, "", 0},
		{"Bagian dari perjanjian ini", false, 0, "", 0},
		{"Ketentuan Umum", false, 0, "", 0},
		{"", false, 0, "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseHeading(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ParseHeading(%q) ok = %v, want %v (%+v)", tc.in, ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Kind != tc.kind || got.Number != tc.number || got.Ordinal != tc.ordinal {
				t.Errorf("got kind=%v number=%q ordinal=%d, want kind=%v number=%q ordinal=%d",
					got.Kind, got.Number, got.Ordinal, tc.kind, tc.number, tc.ordinal)
			}
			if !strings.HasPrefix(tc.in, got.Label) {
				t.Errorf("Label %q is not a prefix of input %q", got.Label, tc.in)
			}
		})
	}
}

// A paragraph starting with an Indonesian company prefix must not be read as a
// list marker. "PT" parses as uppercase-alpha ordinal 436, which used to invent
// a huruf node and make the numbering validator report every later "PT." as a
// duplicate of it.
func TestCompanyPrefixIsNotAMarker(t *testing.T) {
	doc := docFromLines(t,
		`PT. ABC Sejahtera, a limited liability company incorporated in Indonesia.`,
		`PT. Berau Coal, a limited liability company incorporated in Indonesia.`,
		`CV. Maju Jaya, a partnership incorporated in Indonesia.`,
	)
	Build(doc)

	for id := range doc.Nodes {
		if strings.Contains(id, "PT") || strings.Contains(id, "CV") {
			t.Errorf("company prefix became a structural node: %q", id)
		}
	}
}

// The guard must not break real lists, which reach two-letter markers only by
// counting through the single-letter ones first.
func TestLongListReachesTwoLetterMarkers(t *testing.T) {
	lines := []string{"Pasal 1"}
	for i := 0; i < 27; i++ {
		lines = append(lines, string(rune('a'+i%26))+". isi butir")
	}
	// The 27th item is "aa." in a genuine sequence: it continues an open list.
	lines[len(lines)-1] = "aa. isi butir terakhir"

	doc := docFromLines(t, lines...)
	Build(doc)

	found := false
	for id := range doc.Nodes {
		if strings.HasSuffix(id, ":aa") {
			found = true
		}
	}
	if !found {
		t.Error("two-letter marker continuing an open list was rejected")
	}
}

// docFromLines builds an indexed document from literal paragraph lines.
func docFromLines(t *testing.T, lines ...string) *docmodel.IndexedDoc {
	t.Helper()
	return ingest.ParseTextBytes("test.txt", []byte(strings.Join(lines, "\n")))
}
