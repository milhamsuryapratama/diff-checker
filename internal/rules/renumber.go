package rules

import (
	"fmt"
	"sort"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// A numbering fix has to be planned for the whole document at once.
//
// Each numbering check used to propose its own repair from its own local view,
// and those repairs contradicted each other. On a real NDA whose articles ran
// 2, 2, 4, 8, 6, 8 the checks suggested 2→1, 8→5 and 6→9; applying all three
// leaves 1, 2, 4, 5, 9, 8 — still gapped, still out of order, still duplicated.
// Every individual suggestion was defensible on its own and the set of them was
// wrong, which is worse than proposing nothing: a reviewer who applies them
// trusts the result.
//
// The planner below therefore walks each sequence once and assigns final
// numbers to every member, so the plan is consistent by construction. It is
// pure Go: renumbering is arithmetic over a parsed tree, and asking a model to
// do arithmetic it can only do approximately is how the old pipeline got its
// numbers wrong.

// Renumbering is a document-wide plan to make every sequence run 1, 2, 3, ….
type Renumbering struct {
	// ByParagraph maps a heading's paragraph index to its corrective action.
	// At most one action per paragraph, which is what makes the plan
	// applicable without ordering rules.
	ByParagraph map[int]docmodel.Action

	// Sequences records what each sequence looked like and what it becomes,
	// for the "urutan keseluruhan" summary a reviewer needs in order to trust
	// a set of individually small edits.
	Sequences []SequencePlan
}

// SequencePlan is one numbering run: every Pasal in the document, or every
// ayat inside one Pasal.
type SequencePlan struct {
	Label  string // "Pasal", "Article", "ayat", …
	Scope  string // enclosing article for nested levels; empty when global
	Before []string
	After  []string
	// Fixes counts members whose number actually changes.
	Fixes int
}

// Changed reports whether this sequence needs any edit at all.
func (s SequencePlan) Changed() bool { return s.Fixes > 0 }

// PlanRenumbering computes the corrective plan for a document.
func PlanRenumbering(doc *docmodel.IndexedDoc) *Renumbering {
	plan := &Renumbering{ByParagraph: map[int]docmodel.Action{}}
	if doc == nil || doc.Root == nil {
		return plan
	}

	// Global sequences: articles and chapters number across the whole document
	// rather than restarting inside each parent. Article-level runs are grouped
	// by the document's own heading word, so each language of a multilingual
	// contract is renumbered as its own 1..N run.
	plan.addSequence(collectKind(doc, docmodel.KindBab), "BAB", "")
	for _, r := range articleRuns(doc) {
		plan.addSequence(r.nodes, runLabel(r), "")
	}

	// Marker levels restart inside each parent: every Pasal begins its ayat at 1.
	doc.Root.Walk(func(n *docmodel.Node) {
		for _, group := range markerGroups(n) {
			plan.addSequence(group.nodes, levelLabel(group.kind), scopeOf(n))
		}
	})
	return plan
}

type markerGroup struct {
	kind  docmodel.NodeKind
	nodes []*docmodel.Node
}

// markerGroups splits a node's marker-introduced children into their separate
// lists, using the same {kind, form} grouping the validator uses so the plan
// and the findings always talk about the same sequences.
func markerGroups(n *docmodel.Node) []markerGroup {
	type key struct {
		kind docmodel.NodeKind
		form docmodel.MarkerForm
	}
	order := []key{}
	byKey := map[key][]*docmodel.Node{}

	for _, c := range n.Children {
		if c.Marker == nil {
			continue
		}
		k := key{c.Kind, c.Marker.Form}
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], c)
	}

	out := make([]markerGroup, 0, len(order))
	for _, k := range order {
		out = append(out, markerGroup{kind: k.kind, nodes: byKey[k]})
	}
	return out
}

func scopeOf(n *docmodel.Node) string {
	if n == nil || n.Kind == docmodel.KindRoot {
		return ""
	}
	return n.Path()
}

// addSequence assigns 1..N over one run and records the resulting edits.
func (p *Renumbering) addSequence(nodes []*docmodel.Node, label, scope string) {
	if len(nodes) < 1 {
		return
	}
	ordered := append([]*docmodel.Node(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].HeadIndex < ordered[j].HeadIndex
	})

	sp := SequencePlan{Label: label, Scope: scope}
	for i, n := range ordered {
		want := i + 1
		sp.Before = append(sp.Before, displayNumber(n))

		if currentOrdinal(n) == want {
			sp.After = append(sp.After, displayNumber(n))
			continue
		}
		act := renumberAction(n, want, scope)
		sp.After = append(sp.After, act.New)
		sp.Fixes++

		// One action per paragraph. A heading belongs to exactly one sequence,
		// so a collision here would mean the tree put it in two — worth
		// noticing rather than silently overwriting.
		if _, clash := p.ByParagraph[n.HeadIndex]; !clash {
			p.ByParagraph[n.HeadIndex] = act
		}
	}
	if sp.Fixes > 0 {
		p.Sequences = append(p.Sequences, sp)
	}
}

func currentOrdinal(n *docmodel.Node) int {
	if n.Marker != nil {
		return n.Marker.Ordinal
	}
	return n.Ordinal
}

func displayNumber(n *docmodel.Node) string {
	if n.Marker != nil && n.Marker.Raw != "" {
		return n.Marker.Raw
	}
	return n.Number
}

// Verify re-checks the plan by applying it to a copy of the tree and running
// the numbering validator again.
//
// This is the self-check the pipeline was missing. A plan that still leaves
// defects is not shown as if it were a fix — the remaining problems are
// returned so the caller can say so plainly. Because the plan assigns 1..N by
// construction this should always come back empty; it is here so that a future
// change which breaks that invariant fails loudly instead of quietly emitting
// bad advice again.
func (p *Renumbering) Verify(doc *docmodel.IndexedDoc) []string {
	if doc == nil || doc.Root == nil || len(p.ByParagraph) == 0 {
		return nil
	}

	// Apply the plan to ordinals in a scratch copy of the numbering state.
	saved := map[*docmodel.Node]int{}
	savedNum := map[*docmodel.Node]string{}
	doc.Root.Walk(func(n *docmodel.Node) {
		act, ok := p.ByParagraph[n.HeadIndex]
		if !ok || n.HeadIndex < 0 {
			return
		}
		saved[n] = currentOrdinal(n)
		savedNum[n] = n.Number
		want := targetOrdinal(act, n)
		if n.Marker != nil {
			n.Marker.Ordinal = want
		}
		n.Ordinal = want
		n.Number = act.New
	})
	defer func() {
		for n, v := range saved {
			if n.Marker != nil {
				n.Marker.Ordinal = v
			}
			n.Ordinal = v
			n.Number = savedNum[n]
		}
	}()

	var remaining []string
	for _, f := range ValidateNumbering(doc) {
		remaining = append(remaining, f.Message)
	}
	return remaining
}

// targetOrdinal recovers the numeric target from an action, falling back to the
// node's own position if the rendered form cannot be parsed back.
func targetOrdinal(act docmodel.Action, n *docmodel.Node) int {
	if v, ok := docmodel.ParseArticleNumber(act.New); ok {
		return v
	}
	if m, ok := docmodel.ParseMarker(act.New + " x"); ok {
		return m.Ordinal
	}
	return currentOrdinal(n)
}

// Summary renders the plan as reviewer-facing lines, one per sequence.
func (p *Renumbering) Summary() []string {
	var out []string
	for _, s := range p.Sequences {
		scope := ""
		if s.Scope != "" {
			scope = " dalam " + s.Scope
		}
		out = append(out, fmt.Sprintf("Urutan %s%s: %s → %s",
			s.Label, scope, joinShort(s.Before), joinShort(s.After)))
	}
	return out
}

func joinShort(vals []string) string {
	const max = 12
	if len(vals) <= max {
		return join(vals)
	}
	return join(vals[:max]) + ", …"
}

func join(vals []string) string {
	out := ""
	for i, v := range vals {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
