// Package textdiff computes the text-level differences between two documents.
//
// It replaces two LLM stages from mining-legal-backend at once: the "Diff
// Finder" stage that asks a model to locate changes by paragraph index, and the
// batched "Diff Highlight" stage that asks a model to wrap changed words in
// <b> tags. Both fall out of a standard diff, exactly and for free.
package textdiff

import (
	"html"
	"strings"
	"unicode"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

// Compare aligns two documents at paragraph granularity and returns the changes.
//
// Alignment is a Myers diff over paragraph texts, which gives an optimal
// matching without any heuristics about section identity. Adjacent
// delete/insert runs are then paired into a single "modified" change, because
// that is how a reviewer reads an edit — as a rewrite, not as a removal
// followed by an unrelated addition.
func Compare(prev, curr *docmodel.IndexedDoc) []docmodel.Change {
	if prev == nil || curr == nil {
		return nil
	}

	dmp := diffmatchpatch.New()

	// Paragraph texts are joined with newlines and diffed in line mode, so each
	// rune in the diff stands for one whole paragraph.
	prevJoined := joinParagraphs(prev)
	currJoined := joinParagraphs(curr)

	r1, r2, _ := dmp.DiffLinesToRunes(prevJoined, currJoined)
	diffs := dmp.DiffMainRunes(r1, r2, false)

	type run struct {
		op    diffmatchpatch.Operation
		start int
		count int
	}
	var runs []run

	pi, ci := 0, 0
	for _, d := range diffs {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			pi += n
			ci += n
		case diffmatchpatch.DiffDelete:
			runs = append(runs, run{d.Type, pi, n})
			pi += n
		case diffmatchpatch.DiffInsert:
			runs = append(runs, run{d.Type, ci, n})
			ci += n
		}
	}

	var changes []docmodel.Change
	for i := 0; i < len(runs); i++ {
		r := runs[i]

		// A delete immediately followed by an insert is one rewrite.
		if r.op == diffmatchpatch.DiffDelete && i+1 < len(runs) &&
			runs[i+1].op == diffmatchpatch.DiffInsert {
			ins := runs[i+1]
			changes = append(changes, buildChange(dmp, prev, curr,
				indexRange(r.start, r.count), indexRange(ins.start, ins.count)))
			i++
			continue
		}

		switch r.op {
		case diffmatchpatch.DiffDelete:
			changes = append(changes, buildChange(dmp, prev, curr,
				indexRange(r.start, r.count), nil))
		case diffmatchpatch.DiffInsert:
			changes = append(changes, buildChange(dmp, prev, curr,
				nil, indexRange(r.start, r.count)))
		}
	}

	// Drop changes that turned out to be entirely blank paragraphs — an edit that
	// only adds or removes empty lines is not something a reviewer wants to see.
	out := changes[:0]
	for _, c := range changes {
		if strings.TrimSpace(c.PrevText) == "" && strings.TrimSpace(c.CurrText) == "" {
			continue
		}
		c.ID = (len(out) + 1) * 10
		out = append(out, c)
	}
	return out
}

func joinParagraphs(d *docmodel.IndexedDoc) string {
	parts := make([]string, len(d.Paragraphs))
	for i, p := range d.Paragraphs {
		parts[i] = p.Text
	}
	// A trailing newline makes the last paragraph a complete line, so line-mode
	// diffing does not merge it with whatever follows.
	return strings.Join(parts, "\n") + "\n"
}

func indexRange(start, count int) []int {
	if count <= 0 {
		return nil
	}
	out := make([]int, count)
	for i := range out {
		out[i] = start + i
	}
	return out
}

func buildChange(dmp *diffmatchpatch.DiffMatchPatch, prev, curr *docmodel.IndexedDoc,
	prevIdx, currIdx []int) docmodel.Change {

	c := docmodel.Change{
		PrevIndexes: prevIdx,
		CurrIndexes: currIdx,
		PrevText:    textOf(prev, prevIdx),
		CurrText:    textOf(curr, currIdx),
	}

	switch {
	case len(prevIdx) == 0:
		c.Type = docmodel.ChangeAdded
	case len(currIdx) == 0:
		c.Type = docmodel.ChangeRemoved
	default:
		c.Type = docmodel.ChangeModified
	}

	c.PrevHTML, c.CurrHTML = highlight(dmp, c.PrevText, c.CurrText)
	c.Cosmetic = isCosmetic(c.PrevText, c.CurrText)

	// Label the change with where it sits. The current document is authoritative
	// for context; for a pure deletion only the previous document knows.
	if owner := ownerFor(curr, currIdx); owner != nil {
		c.Context, c.NodeID = owner.Path(), owner.ID
	} else if owner := ownerFor(prev, prevIdx); owner != nil {
		c.Context, c.NodeID = owner.Path(), owner.ID
	}

	// A clause inside a schedule is unfindable by paragraph number alone, so
	// the table cell travels with the change.
	c.TableRef = tableRefFor(curr, currIdx)
	if c.TableRef == "" {
		c.TableRef = tableRefFor(prev, prevIdx)
	}
	return c
}

func tableRefFor(doc *docmodel.IndexedDoc, idx []int) string {
	if doc == nil || len(idx) == 0 || idx[0] >= len(doc.Paragraphs) {
		return ""
	}
	return doc.Paragraphs[idx[0]].TableRef()
}

func ownerFor(doc *docmodel.IndexedDoc, idx []int) *docmodel.Node {
	if len(idx) == 0 {
		return nil
	}
	return structure.OwnerOf(doc, idx[0])
}

func textOf(doc *docmodel.IndexedDoc, idx []int) string {
	if len(idx) == 0 {
		return ""
	}
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		if p, ok := doc.Para(i); ok {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, " ")
}

// highlight produces the two rendered sides with changed words wrapped in <b>.
//
// The word-level diff is computed once and read twice: the previous side keeps
// equal and deleted spans, the current side keeps equal and inserted spans.
// Text is HTML-escaped before the tags are added, so document content can never
// inject markup into the report.
func highlight(dmp *diffmatchpatch.DiffMatchPatch, prevText, currText string) (string, string) {
	if prevText == "" && currText == "" {
		return "", ""
	}
	if prevText == "" {
		return "", "<b>" + html.EscapeString(currText) + "</b>"
	}
	if currText == "" {
		return "<b>" + html.EscapeString(prevText) + "</b>", ""
	}

	diffs := trimHighlightEdges(diffWords(dmp, prevText, currText))

	var prevOut, currOut strings.Builder
	for _, d := range diffs {
		esc := html.EscapeString(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			prevOut.WriteString(esc)
			currOut.WriteString(esc)
		case diffmatchpatch.DiffDelete:
			prevOut.WriteString("<b>")
			prevOut.WriteString(esc)
			prevOut.WriteString("</b>")
		case diffmatchpatch.DiffInsert:
			currOut.WriteString("<b>")
			currOut.WriteString(esc)
			currOut.WriteString("</b>")
		}
	}
	return prevOut.String(), currOut.String()
}

// isCosmetic reports whether two texts differ only in punctuation.
//
// Case is deliberately significant: "Pihak Pertama" and "pihak pertama" are a
// defined term versus a common noun in a legal document, which is a real change.
// Whitespace differences are already gone by this point because paragraph text
// is normalized during ingest.
func isCosmetic(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return stripPunct(a) == stripPunct(b)
}

func stripPunct(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(r)
			prevSpace = false
		case !prevSpace:
			out.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(out.String())
}

// Summarize counts the changes by type for the report header.
func Summarize(changes []docmodel.Change) docmodel.Summary {
	var s docmodel.Summary
	for _, c := range changes {
		switch c.Type {
		case docmodel.ChangeAdded:
			s.Added++
		case docmodel.ChangeRemoved:
			s.Removed++
		case docmodel.ChangeModified:
			s.Modified++
		}
	}
	s.Total = len(changes)
	return s
}

// Substantive filters out cosmetic changes. Only these reach the LLM tier,
// which is what makes the "no substantive change" shortcut possible.
func Substantive(changes []docmodel.Change) []docmodel.Change {
	var out []docmodel.Change
	for _, c := range changes {
		if !c.Cosmetic {
			out = append(out, c)
		}
	}
	return out
}
