// Command gen produces the DOCX/PDF fixture pairs under testdata/scenarios.
//
// It lives under testdata/ deliberately: Go's tooling ignores directories
// named "testdata" when expanding "./..." patterns, so this generator never
// gets pulled into `go build ./...`, `go vet ./...`, or `go test ./...`. It is
// run explicitly: `go run ./testdata/gen`.
//
// DOCX files are built by hand as a minimal-but-valid OOXML package — just
// enough parts for Word and LibreOffice to open them, with literal typed
// markers ("(1)", "a.") rather than Word's auto-numbering XML. That mirrors
// how the documents this tool targets are actually authored, and matches
// what internal/ingest parses.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

const (
	contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>`

	rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>`

	docRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

	stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults>
    <w:rPrDefault>
      <w:rPr>
        <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/>
        <w:sz w:val="22"/>
      </w:rPr>
    </w:rPrDefault>
    <w:pPrDefault>
      <w:pPr><w:spacing w:after="160" w:line="276" w:lineRule="auto"/></w:pPr>
    </w:pPrDefault>
  </w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal">
    <w:name w:val="Normal"/>
  </w:style>
</w:styles>`

	coreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
  xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:title>diff-checker fixture</dc:title>
  <dc:creator>diff-checker</dc:creator>
</cp:coreProperties>`

	appXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">
  <Application>diff-checker fixture generator</Application>
</Properties>`
)

// buildDocx assembles a minimal valid .docx from a flat paragraph list.
//
// An empty string produces a blank paragraph (a spacer line), matching the
// convention already used by the .txt fixtures in testdata/pair01. Headings
// (BAB / Pasal / Article / Bagian / all-caps title lines) are rendered bold
// for readability when the file is opened directly — purely cosmetic, it has
// no bearing on how internal/ingest parses the content.
func buildDocx(paras []string) []byte {
	var body strings.Builder
	for _, p := range paras {
		body.WriteString(renderParagraph(p))
	}
	// A4 page, 2.5cm margins, portrait — sectPr must be the last child of body.
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1417" w:right="1417" w:bottom="1417" w:left="1417" w:header="708" w:footer="708" w:gutter="0"/>` +
		`</w:sectPr>`)

	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` + body.String() + `</w:body></w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	must(writeEntry(zw, "[Content_Types].xml", contentTypesXML))
	must(writeEntry(zw, "_rels/.rels", rootRelsXML))
	must(writeEntry(zw, "docProps/core.xml", coreXML))
	must(writeEntry(zw, "docProps/app.xml", appXML))
	must(writeEntry(zw, "word/document.xml", documentXML))
	must(writeEntry(zw, "word/_rels/document.xml.rels", docRelsXML))
	must(writeEntry(zw, "word/styles.xml", stylesXML))
	must(zw.Close())
	return buf.Bytes()
}

func renderParagraph(text string) string {
	if text == "" {
		return `<w:p/>`
	}

	rPr := ""
	pPr := ""
	if isChapterHeading(text) {
		rPr = `<w:rPr><w:b/><w:sz w:val="28"/></w:rPr>`
		pPr = `<w:pPr><w:jc w:val="center"/></w:pPr>`
	} else if isChapterTitle(text) {
		rPr = `<w:rPr><w:b/><w:sz w:val="26"/></w:rPr>`
		pPr = `<w:pPr><w:jc w:val="center"/></w:pPr>`
	} else if isArticleHeading(text) {
		rPr = `<w:rPr><w:b/></w:rPr>`
	}

	run := `<w:r>`
	if rPr != "" {
		run += rPr
	}
	run += `<w:t xml:space="preserve">` + escapeXML(text) + `</w:t></w:r>`
	return `<w:p>` + pPr + run + `</w:p>`
}

func isChapterHeading(s string) bool {
	return strings.HasPrefix(s, "BAB ")
}

// isChapterTitle recognizes the all-caps caption line under a BAB heading
// ("KETENTUAN UMUM"). A light heuristic is enough here since this only
// affects cosmetic rendering.
func isChapterTitle(s string) bool {
	if len(s) < 3 || len(s) > 60 {
		return false
	}
	letters, upper := 0, 0
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			letters++
		case r >= 'A' && r <= 'Z':
			letters++
			upper++
		}
	}
	return letters >= 3 && upper == letters
}

func isArticleHeading(s string) bool {
	for _, p := range []string{"Pasal ", "Article ", "Clause ", "Bagian ", "Paragraf "} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func escapeXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func writeEntry(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(content))
	return err
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("gen: %v", err))
	}
}
