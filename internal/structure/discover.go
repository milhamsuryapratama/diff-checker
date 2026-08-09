package structure

import (
	"regexp"
	"sort"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// The named-heading patterns in headings.go know a fixed vocabulary: BAB,
// Bagian, Paragraf, Pasal, Article, Clause, Section. That covers Indonesian and
// English drafting and nothing else — a Dutch "Artikel", a French "Article
// premier", a Malay "Fasal", a German "Paragraph" all fall through and the
// document parses as a flat list with no structure at all, which silently
// disables every check built on the tree.
//
// A fixed list cannot be extended far enough to be safe: the product accepts
// whatever a customer uploads. So the vocabulary is learned from the document
// instead. A heading keyword has a shape that survives translation — a short
// word, repeated at the start of many paragraphs, each time followed by a
// number, and those numbers ascend. Prose does not do that. Neither does a
// citation ("sebagaimana dimaksud dalam Pasal 12"), because it is not at the
// start of the paragraph.
//
// Discovery only ever *adds* keywords. Anything the fixed patterns already
// recognise is matched by them first, so a document using known vocabulary
// parses exactly as before.

// reCandidate captures a leading word followed by a number: "Artikel 5",
// "Fasal 12", "Статья 3". The word must be alphabetic in some script and short
// enough to be a label rather than a sentence opening.
var reCandidate = regexp.MustCompile(
	`^\s*(\p{L}{2,15})\s+(\d{1,4}(?:\.\d{1,3})*|[IVXLCDM]{1,7})\s*[.:)-]?\s*$`)

// Discovery thresholds.
//
// A keyword must introduce several paragraphs before it is believed, and its
// numbers must mostly ascend. Both guards exist because the cost of a false
// positive is high: promoting an ordinary word to a heading level would
// restructure the whole document around it.
const (
	minKeywordHits    = 3
	minAscendingRatio = 0.7
)

// discoveredHeadings maps a lowercased keyword to the node kind it should
// produce. It is computed per document.
type discoveredHeadings map[string]docmodel.NodeKind

// discoverHeadings learns which words act as structural headings in this
// document.
//
// Returns nil when the document's headings are already covered by the known
// patterns, which is the common case and costs one pass over the paragraphs.
func discoverHeadings(doc *docmodel.IndexedDoc) discoveredHeadings {
	type candidate struct {
		ordinals []int
		firstAt  int
	}
	seen := map[string]*candidate{}

	for i, p := range doc.Paragraphs {
		if p.IsBlank() {
			continue
		}
		// Anything the fixed patterns already handle is not a candidate: the
		// known vocabulary keeps its established meaning.
		if _, ok := ParseHeading(p.Text); ok {
			continue
		}
		m := reCandidate.FindStringSubmatch(p.Text)
		if m == nil {
			continue
		}
		word := strings.ToLower(m[1])
		ord, ok := headingOrdinal(m[2])
		if !ok {
			continue
		}
		c := seen[word]
		if c == nil {
			c = &candidate{firstAt: i}
			seen[word] = c
		}
		c.ordinals = append(c.ordinals, ord)
	}

	out := discoveredHeadings{}
	for word, c := range seen {
		if len(c.ordinals) < minKeywordHits {
			continue
		}
		if ascendingRatio(c.ordinals) < minAscendingRatio {
			continue
		}
		out[word] = docmodel.KindArticle
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ascendingRatio is the share of consecutive pairs that increase.
//
// Real heading runs climb. A word that happens to precede numbers at random —
// a currency, a unit, a defined term — does not, and a document whose headings
// were shuffled is broken in a way this check should not paper over.
func ascendingRatio(ords []int) float64 {
	if len(ords) < 2 {
		return 0
	}
	up := 0
	for i := 1; i < len(ords); i++ {
		if ords[i] > ords[i-1] {
			up++
		}
	}
	return float64(up) / float64(len(ords)-1)
}

func headingOrdinal(num string) (int, bool) {
	return docmodel.ParseArticleNumber(num)
}

// parseDiscovered recognises a heading using the document's learned vocabulary.
func (d discoveredHeadings) parse(text string) (Heading, bool) {
	if len(d) == 0 {
		return Heading{}, false
	}
	m := reCandidate.FindStringSubmatch(text)
	if m == nil {
		return Heading{}, false
	}
	kind, ok := d[strings.ToLower(m[1])]
	if !ok {
		return Heading{}, false
	}
	ord, ok := headingOrdinal(m[2])
	if !ok {
		return Heading{}, false
	}
	label := strings.TrimSpace(m[0])
	return Heading{
		Kind:      kind,
		Number:    m[2],
		Ordinal:   ord,
		Label:     label,
		PrefixLen: len(m[0]),
	}, true
}

// Keywords lists the learned vocabulary, for diagnostics.
func (d discoveredHeadings) Keywords() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
