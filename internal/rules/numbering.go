// Package rules holds the deterministic validators: numbering integrity and
// cross-reference integrity.
//
// Every finding produced here is docmodel.ClassVerified. Nothing in this package
// calls a model, so its output is reproducible and cannot be hallucinated. In
// mining-legal-backend both of these checks are LLM stages; here they are
// algorithms over the parsed tree.
package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// ValidateNumbering checks that every numbered sequence in the document counts
// correctly: starts where it should, has no gaps, no duplicates, and does not
// run backwards.
func ValidateNumbering(doc *docmodel.IndexedDoc) []docmodel.Finding {
	if doc == nil || doc.Root == nil {
		return nil
	}
	var out []docmodel.Finding

	// Article and chapter numbering runs across the whole document, not within a
	// parent: Pasal 3 may live under BAB II while Pasal 1 and 2 live under BAB I,
	// and the sequence 1,2,3 still has to hold.
	out = append(out, checkGlobalSequence(doc, docmodel.KindBab, "BAB")...)
	out = append(out, checkGlobalArticles(doc)...)

	// Marker levels are scoped to their parent: each Pasal restarts its ayat at 1.
	doc.Root.Walk(func(n *docmodel.Node) {
		out = append(out, checkChildGroups(n)...)
	})

	return out
}

// collectKind gathers every node of a kind in document order.
func collectKind(doc *docmodel.IndexedDoc, kind docmodel.NodeKind) []*docmodel.Node {
	var out []*docmodel.Node
	doc.Root.Walk(func(n *docmodel.Node) {
		if n.Kind == kind {
			out = append(out, n)
		}
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].HeadIndex < out[j].HeadIndex })
	return out
}

func checkGlobalSequence(doc *docmodel.IndexedDoc, kind docmodel.NodeKind, label string) []docmodel.Finding {
	return checkSequence(collectKind(doc, kind), label, "")
}

// checkGlobalArticles validates Pasal and Article as one sequence, since a
// bilingual document numbers them in parallel rather than continuing one another.
func checkGlobalArticles(doc *docmodel.IndexedDoc) []docmodel.Finding {
	var out []docmodel.Finding
	out = append(out, checkSequence(collectKind(doc, docmodel.KindPasal), "Pasal", "")...)
	out = append(out, checkSequence(collectKind(doc, docmodel.KindArticle), "Article", "")...)
	return out
}

// checkChildGroups validates each family of marker-introduced children of n.
func checkChildGroups(n *docmodel.Node) []docmodel.Finding {
	if n == nil || len(n.Children) == 0 {
		return nil
	}

	type key struct {
		kind docmodel.NodeKind
		form docmodel.MarkerForm
	}
	groups := map[key][]*docmodel.Node{}
	var order []key

	for _, c := range n.Children {
		if c.Marker == nil {
			continue // named headings are validated globally
		}
		k := key{c.Kind, c.Marker.Form}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}

	var out []docmodel.Finding
	for _, k := range order {
		g := groups[k]
		out = append(out, checkSequence(g, levelLabel(k.kind), n.Path())...)
		if k.kind == docmodel.KindClause {
			out = append(out, checkDecimalPrefix(n, g)...)
		}
	}
	return out
}

func levelLabel(k docmodel.NodeKind) string {
	switch k {
	case docmodel.KindAyat:
		return "ayat"
	case docmodel.KindHuruf:
		return "huruf"
	case docmodel.KindAngka:
		return "angka"
	case docmodel.KindClause:
		return "klausul"
	case docmodel.KindItem:
		return "butir"
	}
	return k.String()
}

// checkSequence is the core rule: an ordered list must read 1, 2, 3, ...
//
// scope names the container in messages ("Pasal 12") and is empty for
// document-global sequences.
func checkSequence(nodes []*docmodel.Node, label, scope string) []docmodel.Finding {
	if len(nodes) < 2 {
		return nil
	}
	var out []docmodel.Finding

	in := func(s string) string {
		if scope == "" {
			return ""
		}
		return " dalam " + s
	}

	// Duplicates: the same ordinal used twice.
	seen := map[int]*docmodel.Node{}
	for _, n := range nodes {
		if prev, dup := seen[n.Ordinal]; dup {
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatNumberingDuplicate,
				Severity:  docmodel.SeverityMajor,
				NodeID:    n.ID,
				ParaIndex: docmodel.IntPtr(n.HeadIndex),
				Message: fmt.Sprintf("%s %s muncul dua kali%s (sebelumnya di paragraf [%d])",
					label, n.Number, in(scope), prev.HeadIndex),
				Evidence: []string{
					fmt.Sprintf("[%d] %s %s", prev.HeadIndex, label, prev.Number),
					fmt.Sprintf("[%d] %s %s", n.HeadIndex, label, n.Number),
				},
			})
			continue
		}
		seen[n.Ordinal] = n
	}

	// Bad start: a list that begins at something other than 1.
	if first := nodes[0]; first.Ordinal != 1 {
		out = append(out, docmodel.Finding{
			Class:     docmodel.ClassVerified,
			Category:  docmodel.CatNumberingBadStart,
			Severity:  docmodel.SeverityMinor,
			NodeID:    first.ID,
			ParaIndex: docmodel.IntPtr(first.HeadIndex),
			Message: fmt.Sprintf("Urutan %s%s dimulai dari %s, seharusnya dari %s",
				label, in(scope), first.Number,
				docmodel.FormatOrdinal(markerKindOf(first), 1)),
			Actions: []docmodel.Action{renumberAction(first, 1, scope)},
		})
	}

	// Gaps and reversals across consecutive members.
	for i := 1; i < len(nodes); i++ {
		prev, cur := nodes[i-1], nodes[i]
		switch {
		case cur.Ordinal == prev.Ordinal:
			// already reported as a duplicate
		case cur.Ordinal < prev.Ordinal:
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatNumberingOutOfOrder,
				Severity:  docmodel.SeverityMajor,
				NodeID:    cur.ID,
				ParaIndex: docmodel.IntPtr(cur.HeadIndex),
				Message: fmt.Sprintf("%s %s muncul setelah %s %s%s — urutan mundur",
					label, cur.Number, label, prev.Number, in(scope)),
				Actions: []docmodel.Action{renumberAction(cur, prev.Ordinal+1, scope)},
			})
		case cur.Ordinal > prev.Ordinal+1:
			missing := make([]string, 0, cur.Ordinal-prev.Ordinal-1)
			for v := prev.Ordinal + 1; v < cur.Ordinal; v++ {
				missing = append(missing, docmodel.FormatOrdinal(markerKindOf(cur), v))
			}
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatNumberingGap,
				Severity:  docmodel.SeverityMajor,
				NodeID:    cur.ID,
				ParaIndex: docmodel.IntPtr(cur.HeadIndex),
				Message: fmt.Sprintf("%s %s hilang%s — melompat dari %s ke %s",
					label, strings.Join(missing, ", "), in(scope), prev.Number, cur.Number),
				Evidence: []string{fmt.Sprintf("[%d] %s %s", prev.HeadIndex, label, prev.Number),
					fmt.Sprintf("[%d] %s %s", cur.HeadIndex, label, cur.Number)},
				Actions: []docmodel.Action{renumberAction(cur, prev.Ordinal+1, scope)},
			})
		}
	}

	// Mixed marker forms within one list read as two lists to a human.
	if forms := distinctForms(nodes); len(forms) > 1 {
		out = append(out, docmodel.Finding{
			Class:     docmodel.ClassVerified,
			Category:  docmodel.CatNumberingMixedForm,
			Severity:  docmodel.SeverityMinor,
			NodeID:    nodes[0].ID,
			ParaIndex: docmodel.IntPtr(nodes[0].HeadIndex),
			Message: fmt.Sprintf("Urutan %s%s memakai gaya penomoran campuran: %s",
				label, in(scope), strings.Join(forms, ", ")),
		})
	}

	return out
}

func markerKindOf(n *docmodel.Node) docmodel.MarkerKind {
	if n != nil && n.Marker != nil {
		return n.Marker.Kind
	}
	return docmodel.KindDigit
}

func distinctForms(nodes []*docmodel.Node) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range nodes {
		if n.Marker == nil {
			continue
		}
		f := n.Marker.Form.String()
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// renumberAction proposes the concrete edit that fixes a numbering defect.
//
// Old and New quote the marker exactly as written, so the grounding validator
// can confirm the text really appears in the target paragraph before the action
// is ever shown to a user.
func renumberAction(n *docmodel.Node, want int, scope string) docmodel.Action {
	old := n.Number
	newVal := docmodel.FormatOrdinal(markerKindOf(n), want)
	if n.Marker != nil {
		old = n.Marker.Raw
		newVal = docmodel.Render(n.Marker.Kind, n.Marker.Form, want)
	}
	return docmodel.Action{
		Type:           docmodel.ActionRenumber,
		ParagraphIndex: docmodel.IntPtr(n.HeadIndex),
		Old:            old,
		New:            newVal,
		Context:        scope,
		Rationale:      "menyesuaikan urutan penomoran agar berurutan",
	}
}

// checkDecimalPrefix verifies that decimal clauses inherit their article number:
// clauses under Article 2 must be numbered 2.1, 2.2, not 3.1.
func checkDecimalPrefix(parent *docmodel.Node, clauses []*docmodel.Node) []docmodel.Finding {
	if parent == nil || !parent.Kind.IsArticleLevel() {
		return nil
	}
	want := parent.Number
	var out []docmodel.Finding
	for _, c := range clauses {
		if c.Marker == nil || len(c.Marker.Segments) < 2 {
			continue
		}
		got := fmt.Sprintf("%d", c.Marker.Segments[0])
		if got != want {
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatNumberingBadStart,
				Severity:  docmodel.SeverityMajor,
				NodeID:    c.ID,
				ParaIndex: docmodel.IntPtr(c.HeadIndex),
				Message: fmt.Sprintf("Klausul %s berada di bawah %s tetapi diawali %s, seharusnya %s",
					c.Number, parent.Path(), got, want),
			})
		}
	}
	return out
}
