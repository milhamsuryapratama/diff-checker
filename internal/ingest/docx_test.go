package ingest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildDOCX wraps a word/document.xml body into a minimal but valid .docx zip.
func buildDOCX(t *testing.T, bodyXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="` + wNS + `"><w:body>` + bodyXML + `</w:body></w:document>`
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func para(runs ...string) string {
	var b strings.Builder
	b.WriteString("<w:p>")
	for _, r := range runs {
		b.WriteString("<w:r><w:t>" + r + "</w:t></w:r>")
	}
	b.WriteString("</w:p>")
	return b.String()
}

func TestParseDOCXBytes(t *testing.T) {
	body := para("BAB I") +
		para("KETENTUAN UMUM") +
		para("Pasal 1") +
		para("(1) Dalam Peraturan ini yang dimaksud dengan:") +
		para("a. Pertambangan adalah ", "sebagian atau seluruh tahapan kegiatan.")

	doc, err := ParseDOCXBytes("test.docx", buildDOCX(t, body))
	if err != nil {
		t.Fatalf("ParseDOCXBytes: %v", err)
	}
	if got, want := len(doc.Paragraphs), 5; got != want {
		t.Fatalf("got %d paragraphs, want %d: %+v", got, want, doc.Paragraphs)
	}

	// Indexes must be dense and sequential — the whole [N] addressing scheme
	// depends on it.
	for i, p := range doc.Paragraphs {
		if p.Index != i {
			t.Errorf("paragraph %d has Index %d", i, p.Index)
		}
	}

	// Runs inside one w:p must concatenate into a single paragraph, not split.
	if got, want := doc.Paragraphs[4].Text,
		"a. Pertambangan adalah sebagian atau seluruh tahapan kegiatan."; got != want {
		t.Errorf("run concatenation: got %q, want %q", got, want)
	}
	if doc.Lang != "id" {
		t.Errorf("Lang = %q, want id", doc.Lang)
	}
}

// TestParseDOCXTrackedChanges pins the accepted-revisions view: text marked
// deleted must not appear, text marked inserted must.
func TestParseDOCXTrackedChanges(t *testing.T) {
	body := `<w:p>` +
		`<w:r><w:t>Jangka waktu </w:t></w:r>` +
		`<w:del><w:r><w:delText>30 (tiga puluh)</w:delText></w:r></w:del>` +
		`<w:ins><w:r><w:t>60 (enam puluh)</w:t></w:r></w:ins>` +
		`<w:r><w:t> hari kerja.</w:t></w:r>` +
		`</w:p>`

	doc, err := ParseDOCXBytes("tracked.docx", buildDOCX(t, body))
	if err != nil {
		t.Fatalf("ParseDOCXBytes: %v", err)
	}
	got := doc.Paragraphs[0].Text
	if strings.Contains(got, "30 (tiga puluh)") {
		t.Errorf("deleted text leaked into output: %q", got)
	}
	if !strings.Contains(got, "60 (enam puluh)") {
		t.Errorf("inserted text missing from output: %q", got)
	}
	if want := "Jangka waktu 60 (enam puluh) hari kerja."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDOCXTables(t *testing.T) {
	body := para("Sebelum tabel") +
		`<w:tbl><w:tr><w:tc>` + para("Sel pertama") + `</w:tc><w:tc>` + para("Sel kedua") + `</w:tc></w:tr></w:tbl>` +
		para("Setelah tabel")

	doc, err := ParseDOCXBytes("table.docx", buildDOCX(t, body))
	if err != nil {
		t.Fatalf("ParseDOCXBytes: %v", err)
	}
	if got, want := len(doc.Paragraphs), 4; got != want {
		t.Fatalf("got %d paragraphs, want %d", got, want)
	}
	if doc.Paragraphs[0].InTable || doc.Paragraphs[3].InTable {
		t.Error("paragraphs outside the table are marked InTable")
	}
	if !doc.Paragraphs[1].InTable || !doc.Paragraphs[2].InTable {
		t.Error("paragraphs inside the table are not marked InTable")
	}
}

func TestParseDOCXNumberingProps(t *testing.T) {
	body := `<w:p><w:pPr>` +
		`<w:pStyle w:val="ListParagraph"/>` +
		`<w:numPr><w:ilvl w:val="1"/><w:numId w:val="7"/></w:numPr>` +
		`</w:pPr><w:r><w:t>Butir berdaftar</w:t></w:r></w:p>`

	doc, err := ParseDOCXBytes("num.docx", buildDOCX(t, body))
	if err != nil {
		t.Fatalf("ParseDOCXBytes: %v", err)
	}
	p := doc.Paragraphs[0]
	if p.Style != "ListParagraph" {
		t.Errorf("Style = %q, want ListParagraph", p.Style)
	}
	if p.ListID != 7 || p.ListLvl != 1 {
		t.Errorf("ListID/ListLvl = %d/%d, want 7/1", p.ListID, p.ListLvl)
	}
}

func TestParseDOCXTabsAndBreaks(t *testing.T) {
	body := `<w:p><w:r><w:t>Kolom A</w:t><w:tab/><w:t>Kolom B</w:t><w:br/><w:t>Baris baru</w:t></w:r></w:p>`
	doc, err := ParseDOCXBytes("tabs.docx", buildDOCX(t, body))
	if err != nil {
		t.Fatalf("ParseDOCXBytes: %v", err)
	}
	// Normalization collapses the tab and break into single spaces.
	if got, want := doc.Paragraphs[0].Text, "Kolom A Kolom B Baris baru"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(doc.Paragraphs[0].Raw, "\t") {
		t.Errorf("Raw should preserve the tab: %q", doc.Paragraphs[0].Raw)
	}
}

func TestParseDOCXNotAZip(t *testing.T) {
	if _, err := ParseDOCXBytes("bad.docx", []byte("this is not a zip")); err == nil {
		t.Fatal("expected an error for non-zip input")
	}
}

func TestParseDOCXMissingDocumentXML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/other.xml")
	_, _ = w.Write([]byte("<x/>"))
	_ = zw.Close()

	_, err := ParseDOCXBytes("empty.docx", buf.Bytes())
	if err == nil {
		t.Fatal("expected an error when word/document.xml is absent")
	}
	if !strings.Contains(err.Error(), "document.xml") {
		t.Errorf("error should name the missing part, got: %v", err)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  double   spaces  ", "double spaces"},
		{"tab\there", "tab here"},
		{"line\nbreak", "line break"},
		{"“smart quotes”", `"smart quotes"`},
		{"it’s", "it's"},
		{"en–dash and em—dash", "en-dash and em-dash"},
		{"ellipsis…", "ellipsis..."},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeStripsInvisibles(t *testing.T) {
	in := "Pasal" + string(zeroWidthSpace) + " " + string(softHyphen) + "12"
	if got, want := Normalize(in), "Pasal 12"; got != want {
		t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
	}
}

func TestParseTextBytes(t *testing.T) {
	doc := ParseTextBytes("x.txt", []byte("Pasal 1\n\nIsi pasal.\n\n\n"))
	if got, want := len(doc.Paragraphs), 3; got != want {
		t.Fatalf("got %d paragraphs, want %d: %+v", got, want, doc.Paragraphs)
	}
	if doc.Paragraphs[1].Text != "" {
		t.Errorf("blank line should produce an empty paragraph, got %q", doc.Paragraphs[1].Text)
	}
	if doc.Paragraphs[2].Text != "Isi pasal." {
		t.Errorf("got %q", doc.Paragraphs[2].Text)
	}
}

func TestDetectLanguage(t *testing.T) {
	id := DetectLanguage([]string{"Setiap orang yang melakukan kegiatan wajib memiliki izin"})
	if id != "id" {
		t.Errorf("DetectLanguage(indonesian) = %q, want id", id)
	}
	en := DetectLanguage([]string{"The party shall provide any such notice under this agreement"})
	if en != "en" {
		t.Errorf("DetectLanguage(english) = %q, want en", en)
	}
	if got := DetectLanguage([]string{"12345 67890"}); got != "" {
		t.Errorf("DetectLanguage(no signal) = %q, want empty", got)
	}
}
