// Package structure turns a flat list of paragraphs into a document tree:
// named headings (BAB, Bagian, Paragraf, Pasal, Article) and marker-introduced
// levels (ayat, huruf, angka, decimal clauses), plus every cross-reference.
//
// Everything here is deterministic. This package is the replacement for the two
// "Structure Side" LLM calls and the "Structure Comparison" call in
// mining-legal-backend, whose prompts spend roughly 6,000 tokens per document
// describing rules that are expressed here as code and covered by table tests.
package structure

import (
	"regexp"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// Heading is a named structural heading recognized at the start of a paragraph.
type Heading struct {
	Kind docmodel.NodeKind
	// Number is the ordinal as written: "12", "III", "1.1", "Kesatu".
	Number string
	// Ordinal is the numeric value of Number.
	Ordinal int
	// Label is the full heading text, e.g. "Pasal 12".
	Label string
	// PrefixLen is the byte length of the heading within the paragraph, used to
	// stop the reference extractor from reading a heading as a self-reference.
	PrefixLen int
}

var (
	reBab = regexp.MustCompile(`(?i)^\s*BAB\s+([IVXLCDM]{1,7}|\d{1,3})\b`)
	// "Bagian Kesatu" (ordinal word) or "Bagian I" / "Bagian 2".
	reBagian = regexp.MustCompile(`(?i)^\s*Bagian\s+([A-Za-z]+(?:\s+[A-Za-z]+)?|[IVXLCDM]{1,7}|\d{1,3})\b`)
	// "Paragraf 3" — a heading level in Indonesian drafting, not a text paragraph.
	reParagraf = regexp.MustCompile(`(?i)^\s*Paragraf\s+(\d{1,3}|[IVXLCDM]{1,7})\b`)
	rePasal    = regexp.MustCompile(`(?i)^\s*Pasal\s+(\d{1,4}(?:\.\d{1,3})*|[IVXLCDM]{1,7})\b`)
	reArticle  = regexp.MustCompile(`(?i)^\s*(?:Article|Clause|Section)\s+(\d{1,4}(?:\.\d{1,3})*|[IVXLCDM]{1,7})\b`)
)

// indonesianOrdinals maps the ordinal words used for "Bagian" headings.
var indonesianOrdinals = map[string]int{
	"pertama": 1, "kesatu": 1, "kedua": 2, "ketiga": 3, "keempat": 4,
	"kelima": 5, "keenam": 6, "ketujuh": 7, "kedelapan": 8, "kesembilan": 9,
	"kesepuluh": 10, "kesebelas": 11, "kedua belas": 12, "ketiga belas": 13,
	"keempat belas": 14, "kelima belas": 15, "keenam belas": 16,
	"ketujuh belas": 17, "kedelapan belas": 18, "kesembilan belas": 19,
	"kedua puluh": 20,
}

// ParseHeading recognizes a named heading at the start of a paragraph.
//
// Matching is anchored at the start and requires a word boundary after the
// number, so "Pasal 12" is a heading while "dalam Pasal 12 disebutkan" is not.
func ParseHeading(text string) (Heading, bool) {
	type candidate struct {
		re   *regexp.Regexp
		kind docmodel.NodeKind
	}
	// Order matters only in that each pattern is anchored and mutually exclusive.
	for _, c := range []candidate{
		{reBab, docmodel.KindBab},
		{reBagian, docmodel.KindBagian},
		{reParagraf, docmodel.KindParagraf},
		{rePasal, docmodel.KindPasal},
		{reArticle, docmodel.KindArticle},
	} {
		m := c.re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		num := strings.TrimSpace(m[1])
		h := Heading{
			Kind:      c.kind,
			Number:    num,
			Label:     strings.TrimSpace(m[0]),
			PrefixLen: len(m[0]),
		}

		if c.kind == docmodel.KindBagian {
			// "Bagian Kesatu" — resolve the ordinal word. A word that is not a
			// known ordinal means this is prose ("Bagian dari perjanjian ini"),
			// not a heading.
			key := strings.ToLower(num)
			if v, ok := indonesianOrdinals[key]; ok {
				h.Ordinal = v
			} else if v, ok := docmodel.ParseArticleNumber(num); ok {
				h.Ordinal = v
			} else {
				continue
			}
			return h, true
		}

		v, ok := docmodel.ParseArticleNumber(num)
		if !ok {
			continue
		}
		h.Ordinal = v
		return h, true
	}
	return Heading{}, false
}

// IsHeadingOnly reports whether the paragraph contains nothing but the heading.
//
// In Indonesian regulations "Pasal 12" sits on its own line and the body follows
// in later paragraphs; in contracts the heading and body often share a line
// ("Pasal 1.1 Definisi. Dalam perjanjian ini..."). Both shapes must parse.
func IsHeadingOnly(text string, h Heading) bool {
	return strings.TrimSpace(text[h.PrefixLen:]) == ""
}

// isTitleCase reports whether a paragraph looks like a heading caption — the
// all-caps line that follows "BAB I" ("KETENTUAN UMUM").
func isTitleCase(s string) bool {
	if s == "" {
		return false
	}
	letters, upper := 0, 0
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			letters++
		} else if r >= 'A' && r <= 'Z' {
			letters++
			upper++
		}
	}
	if letters < 3 {
		return false
	}
	return upper*100/letters >= 80
}
