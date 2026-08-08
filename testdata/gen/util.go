package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// renumberHeadings rewrites sequential "<prefix>N" headings to start at
// start, leaving every other paragraph untouched.
//
// This lets one boilerplate "tail" template (closing chapter of a contract)
// be reused across every scenario while keeping document-global article
// numbering continuous from wherever that scenario's own content left off —
// prev and curr often need different starting numbers, since the scenario
// under test may itself add or remove an article.
func renumberHeadings(paras []string, prefix string, start int) []string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `\d+$`)
	out := make([]string, len(paras))
	n := start
	for i, p := range paras {
		if re.MatchString(p) {
			out[i] = fmt.Sprintf("%s%d", prefix, n)
			n++
		} else {
			out[i] = p
		}
	}
	return out
}

// renumberChapter rewrites the tail's single "BAB <roman>" heading to
// continue the scenario's own chapter sequence, so the shared closing
// chapter never collides with — or leaves a gap after — chapters the
// scenario itself defines.
func renumberChapter(paras []string, chapterNum int) []string {
	re := regexp.MustCompile(`^BAB [IVXLCDM]+$`)
	out := make([]string, len(paras))
	for i, p := range paras {
		if re.MatchString(p) {
			out[i] = "BAB " + toRoman(chapterNum)
		} else {
			out[i] = p
		}
	}
	return out
}

func toRoman(n int) string {
	vals := []struct {
		v int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, val := range vals {
		for n >= val.v {
			b.WriteString(val.s)
			n -= val.v
		}
	}
	return b.String()
}

// renumberArticleClauses rewrites both "Article N" headings and the leading
// "N.M" decimal clause prefix on the paragraphs under each one, so the
// shared English-style closing tail keeps its clause numbers consistent
// with whatever article number it lands on — renumberHeadings alone only
// rewrites the heading line, leaving every "900.1"-prefixed clause dangling.
func renumberArticleClauses(paras []string, start int) []string {
	reArticle := regexp.MustCompile(`^Article \d+$`)
	reClause := regexp.MustCompile(`^\d+(\.\d+ .*)$`)
	out := make([]string, len(paras))
	n := start
	for i, p := range paras {
		switch {
		case reArticle.MatchString(p):
			out[i] = fmt.Sprintf("Article %d", n)
			n++
		case reClause.MatchString(p):
			m := reClause.FindStringSubmatch(p)
			out[i] = fmt.Sprintf("%d%s", n-1, m[1])
		default:
			out[i] = p
		}
	}
	return out
}

// writeDocx writes a paragraph list as a .docx file.
func writeDocx(path string, paras []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buildDocx(paras), 0o644)
}

// convertToPDF shells out to LibreOffice in headless mode. Each call gets its
// own profile directory: soffice serializes on a shared profile lock, and
// running conversions back-to-back with the default profile intermittently
// hangs waiting for a "instance already running" check to clear.
func convertToPDF(docxPath, outDir string) error {
	profile, err := os.MkdirTemp("", "soffice-profile-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "soffice",
		"--headless", "--norestore", "--nolockcheck", "--nodefault",
		"-env:UserInstallation=file://"+profile,
		"--convert-to", "pdf", "--outdir", outDir, docxPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("soffice convert %s: %w\n%s", docxPath, err, out)
	}

	// soffice's wrapper script exits 0 even when the underlying conversion
	// failed (e.g. "Error: source file could not be loaded") — the only
	// reliable signal is whether the output file actually landed.
	pdfPath := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(docxPath), filepath.Ext(docxPath))+".pdf")
	if _, statErr := os.Stat(pdfPath); statErr != nil {
		return fmt.Errorf("soffice convert %s: no output produced\n%s", docxPath, out)
	}
	return nil
}
