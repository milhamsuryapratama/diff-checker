package textdiff

import (
	"strings"
	"unicode"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// diffWords computes a diff whose smallest unit is a word, not a character.
//
// Character-level diffing produces highlights that split tokens apart: changing
// "30" to "60" marks only the leading digit, because "0" is common to both, and
// a reviewer sees "<b>3</b>0" instead of "<b>30</b>". Legal review is read at
// word granularity, so the diff is computed there.
//
// The technique is the one diffmatchpatch uses for line mode: map each distinct
// token to a single rune, diff the rune sequences, then map back. Tokens include
// the whitespace and punctuation between words, so concatenating them
// reconstructs the input exactly.
func diffWords(dmp *diffmatchpatch.DiffMatchPatch, a, b string) []diffmatchpatch.Diff {
	ra, rb, tokens := wordsToRunes(a, b)
	diffs := dmp.DiffMainRunes(ra, rb, false)
	return mergeAdjacent(runesToWords(diffs, tokens))
}

// tokenize splits text into word tokens and single-character separators.
//
// Runs of letters and digits form one token; every other character stands
// alone. Keeping separators as tokens means the token stream is lossless.
func tokenize(s string) []string {
	var out []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
			continue
		}
		flush()
		out = append(out, string(r))
	}
	flush()
	return out
}

// Unicode surrogate code points cannot appear in a valid Go string, so token
// indices skip over that range when they are encoded as runes.
const (
	surrogateStart = 0xD800
	surrogateSize  = 0x800
)

func indexToRune(i int) rune {
	if i >= surrogateStart {
		return rune(i + surrogateSize)
	}
	return rune(i)
}

func runeToIndex(r rune) int {
	if r >= surrogateStart+surrogateSize {
		return int(r) - surrogateSize
	}
	return int(r)
}

// wordsToRunes encodes both inputs against a shared token table.
func wordsToRunes(a, b string) ([]rune, []rune, []string) {
	tokens := []string{""} // index 0 is reserved so real tokens start at 1
	index := map[string]int{}

	encode := func(s string) []rune {
		toks := tokenize(s)
		out := make([]rune, 0, len(toks))
		for _, tok := range toks {
			i, ok := index[tok]
			if !ok {
				i = len(tokens)
				index[tok] = i
				tokens = append(tokens, tok)
			}
			out = append(out, indexToRune(i))
		}
		return out
	}

	ra := encode(a)
	rb := encode(b)
	return ra, rb, tokens
}

// runesToWords decodes a rune-level diff back into text.
func runesToWords(diffs []diffmatchpatch.Diff, tokens []string) []diffmatchpatch.Diff {
	out := make([]diffmatchpatch.Diff, 0, len(diffs))
	for _, d := range diffs {
		var b strings.Builder
		for _, r := range d.Text {
			i := runeToIndex(r)
			if i > 0 && i < len(tokens) {
				b.WriteString(tokens[i])
			}
		}
		out = append(out, diffmatchpatch.Diff{Type: d.Type, Text: b.String()})
	}
	return out
}

// mergeAdjacent joins consecutive diffs of the same type, which decoding can
// leave behind, so the rendered output does not emit two <b> spans in a row.
func mergeAdjacent(diffs []diffmatchpatch.Diff) []diffmatchpatch.Diff {
	if len(diffs) == 0 {
		return diffs
	}
	out := diffs[:0]
	for _, d := range diffs {
		if d.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Type == d.Type {
			out[n-1].Text += d.Text
			continue
		}
		out = append(out, d)
	}
	return out
}

// trimHighlightEdges moves whitespace that sits at the edge of a changed span
// out of the span, so a highlight never renders as a trailing blank block.
func trimHighlightEdges(diffs []diffmatchpatch.Diff) []diffmatchpatch.Diff {
	for i := range diffs {
		if diffs[i].Type == diffmatchpatch.DiffEqual {
			continue
		}
		diffs[i].Text = strings.TrimRight(diffs[i].Text, " ")
	}
	return diffs
}
