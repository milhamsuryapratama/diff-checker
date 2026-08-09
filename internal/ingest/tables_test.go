package ingest

import "testing"

// A reviewer cannot find "paragraph 213" in a schedule; they can find
// "tabel 2 baris 3 kolom 2".
func TestTableCoordinates(t *testing.T) {
	doc, err := ParseDOCXBytes("t.docx", buildDOCX(t, tableBody()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range doc.Paragraphs {
		if p.Text != "" {
			got[p.Text] = p.TableRef()
		}
	}
	want := map[string]string{
		"sebelum tabel": "",
		"t1r1c1":        "tabel 1 baris 1 kolom 1",
		"t1r1c2":        "tabel 1 baris 1 kolom 2",
		"t1r2c1":        "tabel 1 baris 2 kolom 1",
		"nested":        "tabel 2 baris 1 kolom 1",
		"setelah tabel": "",
		"t3r1c1":        "tabel 3 baris 1 kolom 1",
	}
	for text, wantRef := range want {
		if got[text] != wantRef {
			t.Errorf("%q: TableRef = %q, want %q", text, got[text], wantRef)
		}
	}
}

// tableBody makes a body with a table, a table nested inside it, and a second
// top-level table afterwards.
func tableBody() string {
	p := func(s string) string {
		return `<w:p><w:r><w:t>` + s + `</w:t></w:r></w:p>`
	}
	cell := func(inner string) string { return `<w:tc>` + inner + `</w:tc>` }
	row := func(inner string) string { return `<w:tr>` + inner + `</w:tr>` }
	tbl := func(inner string) string { return `<w:tbl>` + inner + `</w:tbl>` }

	nested := tbl(row(cell(p("nested"))))
	body := p("sebelum tabel") +
		tbl(
			row(cell(p("t1r1c1"))+cell(p("t1r1c2")))+
				row(cell(p("t1r2c1")+nested)),
		) +
		p("setelah tabel") +
		tbl(row(cell(p("t3r1c1"))))
	return body
}
