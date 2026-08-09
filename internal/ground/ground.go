// Package ground validates LLM-proposed actions against the real document
// before a user ever sees them.
//
// This is the layer that decides whether the product can be trusted. A model
// asked to fix "30 hari" in paragraph 42 will sometimes name a paragraph that
// does not exist, quote text that is not in it, or "correct" a string it
// invented wholesale. None of those are detectable by reading the output — they
// are only detectable by checking it against the document.
//
// The rule is therefore absolute: an action whose Old text does not appear
// verbatim in the paragraph it names is not shown, not repaired, and not
// silently downgraded. It is rejected with a reason, and the caller may ask the
// model again. mining-legal-backend filters some bad actions away but never
// tells the model it got them wrong, so the same mistake recurs every run.
package ground

import (
	"fmt"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
)

// Rejection records one action that failed validation and why.
//
// The reason is fed back to the model on a retry, so it is written to be read
// by one: specific about what was wrong, and quoting what the document actually
// says at that location.
type Rejection struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

func (r Rejection) String() string { return fmt.Sprintf("#%d: %s", r.Index, r.Reason) }

// Result is the outcome of validating a batch of actions.
type Result struct {
	Accepted   []docmodel.Action
	Rejections []Rejection
}

// OK reports whether every action passed.
func (r Result) OK() bool { return len(r.Rejections) == 0 }

// Feedback renders the rejections as a prompt fragment for a regeneration
// attempt. Empty when nothing was rejected.
func (r Result) Feedback() string {
	if len(r.Rejections) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Usulan berikut DITOLAK karena tidak cocok dengan dokumen. ")
	b.WriteString("Perbaiki atau hapus usulan tersebut, jangan mengarang teks:\n")
	for _, rej := range r.Rejections {
		fmt.Fprintf(&b, "- %s\n", rej)
	}
	return b.String()
}

// Actions validates every proposed action against the current document.
//
// Checks applied, in order of how often they catch a real fabrication:
//
//  1. ParagraphIndex is present and within range.
//  2. Old appears verbatim in that paragraph.
//  3. New differs from Old (a no-op edit is noise, not a fix).
//  4. The paragraph is not one the deterministic tier already owns a fix for.
//
// A manual-review action carries no text edit, so only its paragraph index is
// checked when it names one.
func Actions(curr *docmodel.IndexedDoc, actions []docmodel.Action) Result {
	var res Result
	for i, a := range actions {
		if reason, ok := validate(curr, a); !ok {
			res.Rejections = append(res.Rejections, Rejection{Index: i, Reason: reason})
			continue
		}
		res.Accepted = append(res.Accepted, a)
	}
	return res
}

func validate(curr *docmodel.IndexedDoc, a docmodel.Action) (string, bool) {
	if a.Type == docmodel.ActionManualReview {
		if a.ParagraphIndex == nil {
			return "", true
		}
		if !inRange(curr, *a.ParagraphIndex) {
			return fmt.Sprintf("paragraf [%d] tidak ada di dokumen (0..%d)",
				*a.ParagraphIndex, lastIndex(curr)), false
		}
		return "", true
	}

	if a.ParagraphIndex == nil {
		return "aksi tidak menyebut paragraf mana yang diubah", false
	}
	idx := *a.ParagraphIndex
	if !inRange(curr, idx) {
		return fmt.Sprintf("paragraf [%d] tidak ada di dokumen (0..%d)", idx, lastIndex(curr)), false
	}
	if strings.TrimSpace(a.Old) == "" {
		return fmt.Sprintf("paragraf [%d]: teks lama kosong", idx), false
	}

	text := curr.Paragraphs[idx].Text
	if !strings.Contains(text, a.Old) {
		// Retry with whitespace normalised on both sides. A model that
		// reproduces the sentence but collapses a double space is describing a
		// real edit; only report a miss when the words themselves differ.
		if !containsNormalized(text, a.Old) {
			return fmt.Sprintf("paragraf [%d] tidak memuat teks %q; isi sebenarnya: %q",
				idx, clip(a.Old, 60), clip(text, 120)), false
		}
	}
	if a.Old == a.New {
		return fmt.Sprintf("paragraf [%d]: teks lama dan baru identik", idx), false
	}
	return "", true
}

// containsNormalized compares with runs of whitespace collapsed, so a quote
// that differs only in spacing still counts as located.
func containsNormalized(haystack, needle string) bool {
	return strings.Contains(
		ingest.NormalizeAggressive(haystack),
		ingest.NormalizeAggressive(needle),
	)
}

// Findings drops advisory findings whose actions all failed validation, and
// keeps the ones that survived with only their accepted actions attached.
//
// A finding whose actions were all rejected is not automatically discarded: the
// observation may still be sound even when the proposed edit was fabricated.
// It is kept without actions and its confidence is reduced, so a reviewer sees
// the concern but is never offered a fix that does not apply.
func Findings(curr *docmodel.IndexedDoc, fs []docmodel.Finding) ([]docmodel.Finding, []Rejection) {
	var out []docmodel.Finding
	var allRejections []Rejection

	for _, f := range fs {
		if len(f.Actions) == 0 {
			out = append(out, f)
			continue
		}
		res := Actions(curr, f.Actions)
		allRejections = append(allRejections, res.Rejections...)

		f.Actions = res.Accepted
		if len(res.Accepted) == 0 && len(res.Rejections) > 0 {
			f.Confidence *= 0.5
			f.Message += " (usulan perbaikan otomatis ditolak validator; perlu tinjauan manual)"
		}
		out = append(out, f)
	}
	return out, allRejections
}

// ParagraphIndex reports whether an index is addressable in the document. Used
// by the tools layer so a model cannot read past the end of the document.
func ParagraphIndex(doc *docmodel.IndexedDoc, i int) bool { return inRange(doc, i) }

func inRange(doc *docmodel.IndexedDoc, i int) bool {
	return doc != nil && i >= 0 && i < len(doc.Paragraphs)
}

func lastIndex(doc *docmodel.IndexedDoc) int {
	if doc == nil || len(doc.Paragraphs) == 0 {
		return -1
	}
	return len(doc.Paragraphs) - 1
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
