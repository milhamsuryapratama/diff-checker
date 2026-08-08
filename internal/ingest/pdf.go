package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// ErrPDFToolMissing is returned when the pdftotext binary is not installed.
var ErrPDFToolMissing = errors.New("pdftotext not found: install poppler-utils to enable PDF input")

// pdfTimeout bounds extraction so a malformed file cannot wedge a worker.
const pdfTimeout = 60 * time.Second

// ParsePDF extracts text from a PDF using pdftotext.
//
// pdftotext is preferred over a pure-Go PDF library because legal documents are
// layout-heavy — multi-column preambles, signature blocks, numbered lists in
// tables — and "-layout" preserves reading order far more faithfully than naive
// content-stream extraction. The cost is a system dependency, which is why the
// Dockerfile installs poppler-utils and this function reports a precise error
// when the binary is absent.
//
// Scanned PDFs contain no text layer and will yield an empty or near-empty
// document. That is detected and reported rather than silently producing a
// document with nothing to compare; OCR is deliberately out of scope for v1.
func ParsePDF(path string) (*docmodel.IndexedDoc, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return nil, ErrPDFToolMissing
	}

	ctx, cancel := context.WithTimeout(context.Background(), pdfTimeout)
	defer cancel()

	// "-layout" keeps physical layout, "-nopgbrk" suppresses form-feed markers
	// that would otherwise become spurious paragraphs at every page boundary.
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-nopgbrk", "-enc", "UTF-8", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pdftotext timed out after %s on %s", pdfTimeout, path)
		}
		return nil, fmt.Errorf("pdftotext failed on %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}

	doc := ParseTextBytes(path, stdout.Bytes())
	if countNonBlank(doc) == 0 {
		return nil, fmt.Errorf("no text layer found in %s: the file is probably a scan, which needs OCR (not supported in v1)", path)
	}
	return doc, nil
}

func countNonBlank(d *docmodel.IndexedDoc) int {
	n := 0
	for _, p := range d.Paragraphs {
		if !p.IsBlank() {
			n++
		}
	}
	return n
}

// ParseText reads a plain-text or Markdown file.
func ParseText(path string) (*docmodel.IndexedDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseTextBytes(path, data), nil
}

// ParseTextBytes splits plain text into paragraphs, one per line.
//
// Line-per-paragraph matches how pdftotext emits content and how the [N]
// addressing scheme is defined for DOCX, so the two ingest paths produce
// comparable documents.
func ParseTextBytes(name string, data []byte) *docmodel.IndexedDoc {
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	paras := make([]docmodel.Paragraph, 0, len(lines))
	for _, line := range lines {
		paras = append(paras, docmodel.Paragraph{
			Index: len(paras),
			Raw:   strings.TrimRight(line, " \t"),
			Text:  Normalize(line),
		})
	}

	// Trailing blank lines carry no content and would only inflate indexes.
	for len(paras) > 0 && paras[len(paras)-1].IsBlank() {
		paras = paras[:len(paras)-1]
	}

	doc := &docmodel.IndexedDoc{Source: name, Paragraphs: paras}
	texts := make([]string, len(paras))
	for i, p := range paras {
		texts[i] = p.Text
	}
	doc.Lang = DetectLanguage(texts)
	return doc
}

// Parse dispatches on file extension.
func Parse(path string) (*docmodel.IndexedDoc, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		return ParseDOCX(path)
	case ".pdf":
		return ParsePDF(path)
	case ".txt", ".md", ".markdown", "":
		return ParseText(path)
	default:
		return nil, fmt.Errorf("unsupported file type %q (supported: .docx, .pdf, .txt, .md)", filepath.Ext(path))
	}
}
