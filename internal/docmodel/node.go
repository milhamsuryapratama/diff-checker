package docmodel

import (
	"sort"
	"strconv"
	"strings"
)

// NodeKind is a level in the document hierarchy.
//
// The Indonesian levels follow the drafting hierarchy codified in UU 12/2011:
// BAB > Bagian > Paragraf > Pasal > ayat > huruf > angka. Note that "Paragraf"
// here is a *heading level*, not a text paragraph — the two are easy to confuse
// and only the heading level is meant by KindParagraf.
type NodeKind uint8

const (
	KindRoot NodeKind = iota
	KindBab
	KindBagian
	KindParagraf
	KindPasal
	KindAyat
	KindHuruf
	KindAngka
	KindArticle // English-language equivalent of Pasal
	KindClause  // decimal-numbered clause: 1.1, 1.1.2
	KindItem    // generic list item that carries a marker but no named level
)

func (k NodeKind) String() string {
	switch k {
	case KindRoot:
		return "root"
	case KindBab:
		return "bab"
	case KindBagian:
		return "bagian"
	case KindParagraf:
		return "paragraf"
	case KindPasal:
		return "pasal"
	case KindAyat:
		return "ayat"
	case KindHuruf:
		return "huruf"
	case KindAngka:
		return "angka"
	case KindArticle:
		return "article"
	case KindClause:
		return "clause"
	case KindItem:
		return "item"
	}
	return "unknown"
}

// Depth is the nesting rank of a kind. Lower binds outer. Used by the tree
// builder to decide whether a new heading nests under or closes the current one.
func (k NodeKind) Depth() int {
	switch k {
	case KindRoot:
		return 0
	case KindBab:
		return 1
	case KindBagian:
		return 2
	case KindParagraf:
		return 3
	case KindPasal, KindArticle:
		return 4
	case KindClause:
		return 5
	case KindAyat:
		return 6
	case KindHuruf:
		return 7
	case KindAngka:
		return 8
	case KindItem:
		return 9
	}
	return 99
}

// IsArticleLevel reports whether the kind is the primary numbered unit that
// cross-references point at (Pasal in Indonesian, Article in English).
func (k NodeKind) IsArticleLevel() bool { return k == KindPasal || k == KindArticle }

// Node is one element of the document structure tree.
type Node struct {
	// ID is a stable path key, e.g. "pasal:12" or "pasal:12/ayat:3".
	ID string `json:"id"`

	Kind NodeKind `json:"kind"`

	// Label is the heading as written, e.g. "Pasal 12" or "BAB III".
	Label string `json:"label,omitempty"`

	// Number is the rendered ordinal as written, e.g. "12", "III", "1.1".
	Number string `json:"number,omitempty"`

	// Ordinal is the numeric value of Number.
	Ordinal int `json:"ordinal,omitempty"`

	// Marker is the parsed list marker for nodes introduced by one (ayat, huruf,
	// angka, clause). Nil for named headings such as Pasal or BAB.
	Marker *Marker `json:"marker,omitempty"`

	// Level is the nesting depth used to build and close the tree.
	//
	// Named headings take fixed levels from NodeKind.Depth. Marker-introduced
	// nodes take their level from where they actually appear, because real
	// documents nest markers freely: a definition list numbered "1., 2." sits
	// directly under a Pasal even though UU 12/2011 calls that level "angka",
	// which is nominally the innermost name. Deriving nesting from position
	// rather than from the level's name is what lets both shapes parse.
	Level int `json:"level"`

	// HeadIndex is the paragraph holding this node's heading or marker.
	HeadIndex int `json:"head_index"`

	// Start and End bound the paragraphs this node covers, inclusive.
	Start int `json:"start"`
	End   int `json:"end"`

	// Title is the caption line following a heading, when present
	// (e.g. "KETENTUAN UMUM" under "BAB I").
	Title string `json:"title,omitempty"`

	Parent   *Node   `json:"-"`
	Children []*Node `json:"children,omitempty"`
}

// Path renders the node as a legal citation, e.g. "Pasal 12 ayat (3) huruf a".
//
// Chapter-level ancestors are deliberately excluded: Indonesian drafting cites
// "Pasal 12 ayat (3)", never "BAB I Pasal 12 ayat (3)". Use FullPath when the
// chapter is wanted for navigation or display.
func (n *Node) Path() string {
	if n == nil {
		return ""
	}
	var parts []string
	for cur := n; cur != nil && cur.Kind != KindRoot; cur = cur.Parent {
		parts = append(parts, cur.citation())
		if cur.Kind.IsArticleLevel() {
			break
		}
	}
	reverse(parts)
	return strings.Join(parts, " ")
}

// FullPath renders the complete ancestry including chapters and parts,
// e.g. "BAB I Pasal 12 ayat (3)". Used for breadcrumbs, not for citations.
func (n *Node) FullPath() string {
	if n == nil {
		return ""
	}
	var parts []string
	for cur := n; cur != nil && cur.Kind != KindRoot; cur = cur.Parent {
		parts = append(parts, cur.citation())
	}
	reverse(parts)
	return strings.Join(parts, " ")
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func (n *Node) citation() string {
	switch n.Kind {
	case KindPasal:
		return "Pasal " + n.Number
	case KindArticle:
		return "Article " + n.Number
	case KindBab:
		return "BAB " + n.Number
	case KindBagian:
		return "Bagian " + n.Number
	case KindParagraf:
		return "Paragraf " + n.Number
	case KindAyat:
		return "ayat (" + n.Number + ")"
	case KindHuruf:
		return "huruf " + n.Number
	case KindAngka:
		return "angka " + n.Number
	case KindClause:
		return n.Number
	}
	if n.Label != "" {
		return n.Label
	}
	return n.Number
}

// Walk calls fn for the node and every descendant, in document order.
func (n *Node) Walk(fn func(*Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// Descendants returns every node beneath n, excluding n itself.
func (n *Node) Descendants() []*Node {
	var out []*Node
	for _, c := range n.Children {
		c.Walk(func(x *Node) { out = append(out, x) })
	}
	return out
}

// Siblings returns the nodes sharing this node's parent and kind, in order.
func (n *Node) Siblings() []*Node {
	if n == nil || n.Parent == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.Parent.Children {
		if c.Kind == n.Kind {
			out = append(out, c)
		}
	}
	return out
}

// AddChild links c under n and keeps Children sorted by document position.
func (n *Node) AddChild(c *Node) {
	c.Parent = n
	n.Children = append(n.Children, c)
	sort.SliceStable(n.Children, func(i, j int) bool {
		return n.Children[i].HeadIndex < n.Children[j].HeadIndex
	})
}

// NodeID builds the stable identifier for a node of the given kind and number,
// scoped under a parent id. Article-level nodes are deliberately rooted at the
// top: "Pasal 12" is globally addressable regardless of which BAB contains it,
// which is exactly how cross-references cite it.
func NodeID(parentID string, kind NodeKind, number string) string {
	seg := kind.String() + ":" + number
	if kind.IsArticleLevel() || kind == KindBab {
		return seg
	}
	if parentID == "" {
		return seg
	}
	return parentID + "/" + seg
}

// FindByCitation resolves a citation triple to a node ID under the article-level
// scope, e.g. ("12", "3", "a", "") -> "pasal:12/ayat:3/huruf:a".
func FindByCitation(nodes map[string]*Node, pasal, ayat, huruf, angka string) (*Node, bool) {
	if pasal == "" {
		return nil, false
	}
	id := "pasal:" + pasal
	if _, ok := nodes[id]; !ok {
		// try the English article level
		alt := "article:" + pasal
		if _, ok2 := nodes[alt]; !ok2 {
			return nil, false
		}
		id = alt
	}
	for _, step := range []struct {
		kind NodeKind
		val  string
	}{{KindAyat, ayat}, {KindHuruf, huruf}, {KindAngka, angka}} {
		if step.val == "" {
			continue
		}
		next := id + "/" + step.kind.String() + ":" + step.val
		if _, ok := nodes[next]; !ok {
			// The parent exists but this sub-level does not: report the deepest
			// node we did resolve so callers can describe the failure precisely.
			n := nodes[id]
			return n, false
		}
		id = next
	}
	n, ok := nodes[id]
	return n, ok
}

// ParseArticleNumber extracts the numeric value of an article-level number,
// tolerating roman ("III"), decimal ("1.1") and plain digit forms.
func ParseArticleNumber(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return n, true
		}
	}
	if v, ok := parseRoman(s); ok {
		return v, true
	}
	return 0, false
}
