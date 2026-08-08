package structure

import (
	"sort"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// Build derives the structure tree and cross-references for a document.
//
// It sets doc.Root, doc.Nodes and doc.References in place. The document's
// paragraphs are not modified.
//
// The algorithm is a single forward pass with a depth stack, followed by three
// small fix-up passes: marker disambiguation per sibling group, range closing,
// and reference extraction. Nothing here consults a model.
func Build(doc *docmodel.IndexedDoc) {
	if doc == nil {
		return
	}

	root := &docmodel.Node{
		ID:        "root",
		Kind:      docmodel.KindRoot,
		HeadIndex: -1,
		Start:     0,
		End:       len(doc.Paragraphs) - 1,
	}
	doc.Root = root
	doc.Nodes = map[string]*docmodel.Node{"root": root}

	// owner[i] is the innermost node covering paragraph i, used later to resolve
	// relative references and to label changes with their structural context.
	owner := make([]*docmodel.Node, len(doc.Paragraphs))

	// headingPrefix[i] is how many leading bytes of paragraph i are its own
	// heading, so reference extraction does not read it as a self-reference.
	headingPrefix := make([]int, len(doc.Paragraphs))

	stack := []*docmodel.Node{root}
	top := func() *docmodel.Node { return stack[len(stack)-1] }

	// attach links n under the correct parent and records it. level is the
	// nesting depth the node should occupy; everything at that level or deeper
	// is closed first.
	attach := func(n *docmodel.Node, level int) {
		n.Level = level
		for len(stack) > 1 && top().Level >= level {
			stack = stack[:len(stack)-1]
		}
		parent := top()
		n.ID = uniqueID(doc.Nodes, docmodel.NodeID(parent.ID, n.Kind, n.Number))
		parent.AddChild(n)
		doc.Nodes[n.ID] = n
		stack = append(stack, n)
	}

	// markerLevel decides where a marker-introduced node belongs.
	//
	// A marker that repeats a symbol family already open in the stack is a
	// sibling of it — "(3)" after "(1) ... a. ... b." closes the huruf list and
	// continues the ayat list. A marker in a family not yet open nests one level
	// deeper than the current node.
	markerLevel := func(m docmodel.Marker) int {
		for i := len(stack) - 1; i >= 1; i-- {
			if om := stack[i].Marker; om != nil && sameFamily(*om, m) {
				return stack[i].Level
			}
		}
		return top().Level + 1
	}

	for i := range doc.Paragraphs {
		p := doc.Paragraphs[i]
		if p.IsBlank() {
			owner[i] = top()
			continue
		}

		if h, ok := ParseHeading(p.Text); ok {
			headingPrefix[i] = h.PrefixLen
			attach(&docmodel.Node{
				Kind:      h.Kind,
				Label:     h.Label,
				Number:    h.Number,
				Ordinal:   h.Ordinal,
				HeadIndex: i,
				Start:     i,
			}, h.Kind.Depth())
			owner[i] = top()
			continue
		}

		if m, ok := docmodel.ParseMarker(p.Text); ok {
			mk := m // copy; the sibling pass may rewrite Kind/Ordinal
			attach(&docmodel.Node{
				Kind:      markerNodeKind(m),
				Number:    m.Key(),
				Ordinal:   m.Ordinal,
				Marker:    &mk,
				HeadIndex: i,
				Start:     i,
			}, markerLevel(m))
			owner[i] = top()
			continue
		}

		// Ordinary body text belongs to the innermost open node. A caption line
		// directly under a named heading becomes that heading's title.
		cur := top()
		owner[i] = cur
		if cur.Title == "" && cur.HeadIndex >= 0 && i == cur.HeadIndex+1 &&
			(cur.Kind == docmodel.KindBab || cur.Kind == docmodel.KindBagian ||
				cur.Kind == docmodel.KindParagraf) && isTitleCase(p.Text) {
			cur.Title = p.Text
		}
	}

	// Marker ambiguity can only be settled once a whole sibling list is visible,
	// which is after the tree exists. Resolving it changes a node's level name
	// ("c." is huruf 3, not item 100), and the name is part of the node ID, so
	// identifiers are assigned only after resolution.
	resolveSiblingMarkers(root)
	reassignIDs(doc)
	closeRanges(root, len(doc.Paragraphs)-1)
	doc.References = extractReferences(doc, owner, headingPrefix)
}

// uniqueID keeps duplicate headings addressable instead of overwriting them.
//
// A document with two "Pasal 12" headings is exactly the defect the numbering
// validator exists to catch, so both nodes must survive into the tree. The first
// occurrence keeps the canonical ID — that is what cross-references resolve
// against — and later ones get a "#2", "#3" suffix.
func uniqueID(nodes map[string]*docmodel.Node, base string) string {
	if _, taken := nodes[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		alt := base + "#" + itoa(n)
		if _, taken := nodes[alt]; !taken {
			return alt
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// markerNodeKind names a marker level from its written form.
//
// This follows UU 12/2011 directly: an ayat is written "(1)", a huruf is written
// "a.", an angka is written "1.". The name is a property of the symbol form, not
// of how deeply the marker happens to be nested — nesting is decided separately
// by markerLevel.
func markerNodeKind(m docmodel.Marker) docmodel.NodeKind {
	switch m.Kind {
	case docmodel.KindDecimal:
		return docmodel.KindClause
	case docmodel.KindLowerAlpha, docmodel.KindUpperAlpha:
		return docmodel.KindHuruf
	case docmodel.KindLowerRoman, docmodel.KindUpperRoman:
		return docmodel.KindItem
	}
	if m.Form == docmodel.FormBracket {
		return docmodel.KindAyat
	}
	return docmodel.KindAngka
}

// sameFamily reports whether two markers belong to the same list: same symbol
// family and same delimiter style. "(1)" and "1." are different lists even
// though both are digits, which is exactly how a reader sees them.
func sameFamily(a, b docmodel.Marker) bool {
	if a.Form != b.Form {
		return false
	}
	if a.Kind == b.Kind {
		return true
	}
	// An unresolved ambiguity should not split a list in two.
	return (a.Ambiguous && a.AltKind == b.Kind) || (b.Ambiguous && b.AltKind == a.Kind)
}

// resolveSiblingMarkers disambiguates roman-versus-alpha readings one sibling
// list at a time, which is the only context in which the question is decidable.
//
// Lists are grouped by nesting level rather than by level name: the name is
// derived from the marker, so before resolution "a." and "c." would be filed
// under different names ("c" reads as roman 100) and would never be compared to
// each other. Level is already correct at this point because the tree builder
// nests by marker family, which tolerates ambiguity.
func resolveSiblingMarkers(n *docmodel.Node) {
	if n == nil {
		return
	}
	byLevel := map[int][]*docmodel.Marker{}
	var levels []int
	for _, c := range n.Children {
		if c.Marker == nil {
			continue
		}
		if _, seen := byLevel[c.Level]; !seen {
			levels = append(levels, c.Level)
		}
		byLevel[c.Level] = append(byLevel[c.Level], c.Marker)
	}
	for _, lv := range levels {
		docmodel.ResolveMarkerSequence(byLevel[lv])
	}

	// Re-derive everything that depends on the now-settled marker.
	for _, c := range n.Children {
		if c.Marker != nil {
			c.Kind = markerNodeKind(*c.Marker)
			c.Ordinal = c.Marker.Ordinal
			c.Number = c.Marker.Key()
		}
		resolveSiblingMarkers(c)
	}
}

// reassignIDs rebuilds every node ID and the lookup map top-down, after marker
// resolution has finalized each node's kind and number.
func reassignIDs(doc *docmodel.IndexedDoc) {
	nodes := map[string]*docmodel.Node{"root": doc.Root}
	var walk func(n *docmodel.Node)
	walk = func(n *docmodel.Node) {
		for _, c := range n.Children {
			c.ID = uniqueID(nodes, docmodel.NodeID(n.ID, c.Kind, c.Number))
			nodes[c.ID] = c
			walk(c)
		}
	}
	walk(doc.Root)
	doc.Nodes = nodes
}

// closeRanges assigns each node the paragraph span it covers, ending just before
// the next node at the same or shallower depth.
func closeRanges(root *docmodel.Node, lastPara int) {
	var flat []*docmodel.Node
	root.Walk(func(n *docmodel.Node) {
		if n.Kind != docmodel.KindRoot {
			flat = append(flat, n)
		}
	})
	sort.SliceStable(flat, func(i, j int) bool { return flat[i].HeadIndex < flat[j].HeadIndex })

	var open []*docmodel.Node
	for _, n := range flat {
		for len(open) > 0 && open[len(open)-1].Level >= n.Level {
			open[len(open)-1].End = n.HeadIndex - 1
			open = open[:len(open)-1]
		}
		open = append(open, n)
	}
	for _, n := range open {
		n.End = lastPara
	}
	// A node can never end before it starts (adjacent headings).
	for _, n := range flat {
		if n.End < n.Start {
			n.End = n.Start
		}
	}
	root.End = lastPara
}

// extractReferences collects every cross-reference and resolves relative ones
// ("ayat (2)") against the article that encloses them.
func extractReferences(doc *docmodel.IndexedDoc, owner []*docmodel.Node, headingPrefix []int) []docmodel.Reference {
	var out []docmodel.Reference
	for i, p := range doc.Paragraphs {
		if p.IsBlank() {
			continue
		}
		refs := docmodel.ExtractReferences(i, p.Text, headingPrefix[i])
		for _, r := range refs {
			if r.Kind == docmodel.RefRelative {
				if art := enclosingArticle(owner[i]); art != nil {
					r.Pasal = art.Number
					if art.Kind == docmodel.KindArticle {
						r.Lang = "en"
					}
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// enclosingArticle walks up to the nearest Pasal or Article.
func enclosingArticle(n *docmodel.Node) *docmodel.Node {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Kind.IsArticleLevel() {
			return cur
		}
	}
	return nil
}

// OwnerOf returns the innermost node covering a paragraph, or nil.
// Used to label a change with its structural context ("Pasal 12 ayat (3)").
func OwnerOf(doc *docmodel.IndexedDoc, paraIndex int) *docmodel.Node {
	if doc == nil || doc.Root == nil {
		return nil
	}
	var best *docmodel.Node
	doc.Root.Walk(func(n *docmodel.Node) {
		if n.Kind == docmodel.KindRoot {
			return
		}
		if paraIndex >= n.Start && paraIndex <= n.End {
			if best == nil || n.Level > best.Level {
				best = n
			}
		}
	})
	return best
}

// ArticleCount counts article-level nodes (Pasal or Article), which is the
// headline structural metric a reviewer checks first.
func ArticleCount(doc *docmodel.IndexedDoc) int {
	if doc == nil || doc.Root == nil {
		return 0
	}
	n := 0
	doc.Root.Walk(func(x *docmodel.Node) {
		if x.Kind.IsArticleLevel() {
			n++
		}
	})
	return n
}
