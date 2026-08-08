// Package ingest turns source files (DOCX, PDF, plain text) into a
// docmodel.IndexedDoc: a flat, [N]-addressable list of paragraphs.
//
// Ingest deliberately does no structural interpretation. It extracts text and
// preserves the original bytes in Paragraph.Raw so that recommendation actions
// can quote text that will actually match when written back to the source file.
// All structural reasoning happens later, in the structure package.
package ingest

import (
	"strings"
	"unicode"
)

// Invisible characters that carry no meaning but appear inconsistently between
// extraction paths. Declared as named constants because they are, by
// definition, impossible to read in source.
const (
	softHyphen         rune = 0x00AD // soft hyphen
	zeroWidthSpace     rune = 0x200B // zero-width space
	zeroWidthNonJoiner rune = 0x200C // zero-width non-joiner
	zeroWidthJoiner    rune = 0x200D // zero-width joiner
	byteOrderMark      rune = 0xFEFF // byte-order mark
	narrowNoBreakSpace rune = 0x202F // narrow no-break space
)

// Normalize produces the comparison form of a paragraph's text.
//
// The goal is that two paragraphs which a human would call identical compare
// equal, without discarding anything that changes meaning. Whitespace is
// collapsed, typographic punctuation is folded to ASCII, and soft hyphens and
// zero-width characters are dropped — these are the artifacts that make DOCX and
// PDF extractions of the same sentence differ.
//
// Case is preserved: "Pihak Pertama" and "pihak pertama" is a real difference
// in a legal document.
func Normalize(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))

	prevSpace := false
	for _, r := range s {
		switch r {
		case softHyphen, zeroWidthSpace, zeroWidthNonJoiner, zeroWidthJoiner, byteOrderMark:
			continue

		// Typographic quotes and dashes fold to ASCII so that a document
		// round-tripped through Word does not read as edited.
		case '‘', '’', '‚', '′': // ' ' ‚ ′
			b.WriteByte('\'')
			prevSpace = false
			continue
		case '“', '”', '„', '″': // " " „ ″
			b.WriteByte('"')
			prevSpace = false
			continue
		case '‐', '‑', '‒', '–', '—', '―', '−': // hyphens, dashes, minus
			b.WriteByte('-')
			prevSpace = false
			continue
		case '…': // ellipsis
			b.WriteString("...")
			prevSpace = false
			continue
		case narrowNoBreakSpace:
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}

		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// NormalizeAggressive additionally strips punctuation and lowercases, for
// similarity matching during alignment. It is never used for display or for
// building recommendation actions.
func NormalizeAggressive(s string) string {
	n := Normalize(s)
	var b strings.Builder
	b.Grow(len(n))
	prevSpace := false
	for _, r := range strings.ToLower(n) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevSpace = false
		case !prevSpace:
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// EqualIgnoringWhitespace reports whether two strings differ only in spacing.
// Used to drop no-op diffs that extraction noise would otherwise produce.
func EqualIgnoringWhitespace(a, b string) bool {
	return Normalize(a) == Normalize(b)
}

// DetectLanguage makes a cheap guess between Indonesian and English from
// function-word frequency. It only needs to be good enough to pick which
// citation vocabulary and which prompt language to use.
func DetectLanguage(paras []string) string {
	idWords := map[string]bool{
		"yang": true, "dan": true, "dengan": true, "untuk": true, "dalam": true,
		"pada": true, "dari": true, "atau": true, "tidak": true, "adalah": true,
		"pasal": true, "ayat": true, "wajib": true, "dapat": true, "sebagaimana": true,
		"dimaksud": true, "ketentuan": true, "peraturan": true, "huruf": true,
	}
	enWords := map[string]bool{
		"the": true, "and": true, "of": true, "to": true, "in": true,
		"shall": true, "any": true, "such": true, "this": true, "agreement": true,
		"article": true, "clause": true, "party": true, "hereof": true, "which": true,
	}

	var id, en int
	for _, p := range paras {
		for _, w := range strings.Fields(strings.ToLower(p)) {
			w = strings.Trim(w, ".,;:()[]\"'")
			if idWords[w] {
				id++
			}
			if enWords[w] {
				en++
			}
		}
	}
	switch {
	case id == 0 && en == 0:
		return ""
	case id >= en:
		return "id"
	default:
		return "en"
	}
}
