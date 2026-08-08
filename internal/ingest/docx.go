package ingest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// wNS is the WordprocessingML namespace. Element names are matched on it so a
// stray element from another namespace cannot be mistaken for document content.
const wNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// ParseDOCX reads a .docx file from disk.
func ParseDOCX(path string) (*docmodel.IndexedDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read docx %s: %w", path, err)
	}
	return ParseDOCXBytes(path, data)
}

// ParseDOCXBytes extracts paragraphs from an in-memory .docx.
//
// Extraction walks word/document.xml as a token stream rather than unmarshalling
// into a struct tree, because the body is a heterogeneous sequence (paragraphs,
// tables, text boxes) whose nesting depth is unbounded.
//
// Tracked changes are resolved to the *accepted* view: text inside w:del is
// dropped, text inside w:ins is kept. Comparing two documents that still carry
// unaccepted revisions would otherwise report differences that no reader sees.
func ParseDOCXBytes(name string, data []byte) (*docmodel.IndexedDoc, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open docx %s: %w", name, err)
	}

	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, fmt.Errorf("open docx %s: word/document.xml not found (is this a .docx?)", name)
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open word/document.xml in %s: %w", name, err)
	}
	defer rc.Close()

	paras, err := extractParagraphs(rc)
	if err != nil {
		return nil, fmt.Errorf("parse word/document.xml in %s: %w", name, err)
	}

	doc := &docmodel.IndexedDoc{Source: name, Paragraphs: paras}
	texts := make([]string, len(paras))
	for i, p := range paras {
		texts[i] = p.Text
	}
	doc.Lang = DetectLanguage(texts)
	return doc, nil
}

// paraState accumulates one w:p as the token stream is walked.
type paraState struct {
	text    strings.Builder
	style   string
	numID   int
	ilvl    int
	hasNum  bool
	active  bool
	inTable bool
}

func extractParagraphs(r io.Reader) ([]docmodel.Paragraph, error) {
	dec := xml.NewDecoder(r)
	// DOCX files in the wild contain entities and quirks that strict mode
	// rejects; content extraction does not depend on strictness.
	dec.Strict = false

	var (
		out      []docmodel.Paragraph
		cur      paraState
		tblDepth int
		skip     int  // >0 while inside a subtree whose text must be ignored
		inText   bool // inside w:t (or w:delText when not skipped)
	)

	emit := func() {
		raw := cur.text.String()
		p := docmodel.Paragraph{
			Index:   len(out),
			Raw:     strings.TrimSpace(raw),
			Text:    Normalize(raw),
			Style:   cur.style,
			InTable: cur.inTable,
		}
		if cur.hasNum {
			p.ListID = cur.numID
			p.ListLvl = cur.ilvl
		}
		out = append(out, p)
		cur = paraState{}
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != wNS {
				continue
			}
			switch t.Name.Local {
			case "tbl":
				tblDepth++
			case "p":
				if !cur.active {
					cur = paraState{active: true, inTable: tblDepth > 0}
				}
			case "del", "instrText", "deleted":
				// w:del wraps deleted runs; w:instrText holds field codes such as
				// page-number and TOC instructions. Neither is reader-visible text.
				skip++
			case "pStyle":
				if v, ok := attr(t, "val"); ok {
					cur.style = v
				}
			case "numId":
				if v, ok := attr(t, "val"); ok {
					if n, err := strconv.Atoi(v); err == nil {
						cur.numID, cur.hasNum = n, true
					}
				}
			case "ilvl":
				if v, ok := attr(t, "val"); ok {
					if n, err := strconv.Atoi(v); err == nil {
						cur.ilvl = n
					}
				}
			case "t":
				if skip == 0 {
					inText = true
				}
			case "tab":
				if skip == 0 && cur.active {
					cur.text.WriteByte('\t')
				}
			case "br", "cr":
				if skip == 0 && cur.active {
					cur.text.WriteByte(' ')
				}
			}

		case xml.CharData:
			if inText && skip == 0 && cur.active {
				cur.text.Write(t)
			}

		case xml.EndElement:
			if t.Name.Space != wNS {
				continue
			}
			switch t.Name.Local {
			case "tbl":
				if tblDepth > 0 {
					tblDepth--
				}
			case "t":
				inText = false
			case "del", "instrText", "deleted":
				if skip > 0 {
					skip--
				}
			case "p":
				if cur.active {
					emit()
				}
			}
		}
	}

	// A trailing unterminated paragraph should still be emitted rather than lost.
	if cur.active {
		emit()
	}
	return out, nil
}

func attr(e xml.StartElement, local string) (string, bool) {
	for _, a := range e.Attr {
		if a.Name.Local == local && (a.Name.Space == wNS || a.Name.Space == "") {
			return a.Value, true
		}
	}
	return "", false
}
