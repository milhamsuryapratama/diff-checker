// Package tools exposes the parsed document to the model as callable
// functions.
//
// This is what separates the pipeline from a prompt chain. The analyze node is
// not handed 25.000 tokens of document text and asked to find things in it; it
// is handed a summary of what changed and given the means to look up whatever
// it decides it needs. A typical change costs ~300 tokens of pulled context
// instead of a full-document prompt, and the model can dig when it is unsure —
// which a static chain cannot do.
//
// Every tool here is read-only and answers from the already-parsed
// docmodel.IndexedDoc. None of them can modify a document, reach the network,
// or touch the filesystem, so a prompt-injected instruction inside an uploaded
// document has nothing here to escalate into.
package tools

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/rules"
)

// Set holds the tools bound to one pair of documents.
type Set struct {
	prev *docmodel.IndexedDoc
	curr *docmodel.IndexedDoc
}

// New binds a tool set to a document pair.
func New(prev, curr *docmodel.IndexedDoc) *Set { return &Set{prev: prev, curr: curr} }

// --- get_paragraph ---------------------------------------------------------

type GetParagraphIn struct {
	Index int `json:"index" jsonschema:"description=paragraph index in the new document"`
	// Radius pulls neighbouring paragraphs too. Two or three is usually enough
	// to tell whether a clause changed meaning.
	Radius int `json:"radius,omitempty" jsonschema:"description=how many paragraphs of context on each side (0-5)"`
}

type Paragraph struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type GetParagraphOut struct {
	Paragraphs []Paragraph `json:"paragraphs"`
	Context    string      `json:"context,omitempty"`
	Error      string      `json:"error,omitempty"`
}

func (s *Set) getParagraph(_ context.Context, in GetParagraphIn) (GetParagraphOut, error) {
	if s.curr == nil || in.Index < 0 || in.Index >= len(s.curr.Paragraphs) {
		return GetParagraphOut{Error: fmt.Sprintf(
			"indeks %d di luar jangkauan (0..%d)", in.Index, len(s.curr.Paragraphs)-1)}, nil
	}
	r := in.Radius
	if r < 0 {
		r = 0
	}
	if r > 5 {
		r = 5
	}
	lo, hi := max(0, in.Index-r), min(len(s.curr.Paragraphs)-1, in.Index+r)

	out := GetParagraphOut{}
	for i := lo; i <= hi; i++ {
		out.Paragraphs = append(out.Paragraphs, Paragraph{Index: i, Text: s.curr.Paragraphs[i].Text})
	}
	if owner := ownerOf(s.curr, in.Index); owner != nil {
		out.Context = owner.Path()
	}
	return out, nil
}

// --- get_section_tree ------------------------------------------------------

type GetSectionTreeIn struct {
	// NodeID scopes the tree, e.g. "pasal:12". Empty returns the top level
	// only, which keeps the reply small on a long document.
	NodeID string `json:"node_id,omitempty" jsonschema:"description=structural node to expand; empty for the top level"`
}

type SectionNode struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Title    string        `json:"title,omitempty"`
	Start    int           `json:"start"`
	End      int           `json:"end"`
	Children []SectionNode `json:"children,omitempty"`
}

type GetSectionTreeOut struct {
	Nodes []SectionNode `json:"nodes"`
	Error string        `json:"error,omitempty"`
}

func (s *Set) getSectionTree(_ context.Context, in GetSectionTreeIn) (GetSectionTreeOut, error) {
	if s.curr == nil || s.curr.Root == nil {
		return GetSectionTreeOut{Error: "struktur dokumen belum tersedia"}, nil
	}
	root := s.curr.Root
	if in.NodeID != "" {
		n, ok := s.curr.Node(in.NodeID)
		if !ok {
			return GetSectionTreeOut{Error: fmt.Sprintf("node %q tidak ditemukan", in.NodeID)}, nil
		}
		root = n
	}
	// Depth 2 keeps a reply bounded on documents with deep nesting; the model
	// can call again with a child ID to go further.
	out := GetSectionTreeOut{}
	for _, c := range root.Children {
		out.Nodes = append(out.Nodes, describe(c, 2))
	}
	return out, nil
}

func describe(n *docmodel.Node, depth int) SectionNode {
	sn := SectionNode{
		ID: n.ID, Label: n.Label, Title: n.Title,
		Start: n.Start, End: n.End,
	}
	if sn.Label == "" {
		sn.Label = n.Kind.String() + " " + n.Number
	}
	if depth > 1 {
		for _, c := range n.Children {
			sn.Children = append(sn.Children, describe(c, depth-1))
		}
	}
	return sn
}

// --- resolve_reference -----------------------------------------------------

type ResolveReferenceIn struct {
	Text string `json:"text" jsonschema:"description=a citation as written, e.g. \"Pasal 12 ayat (3)\""`
}

type ResolveReferenceOut struct {
	Found bool `json:"found"`
	// NodeID and Label identify the target when it resolves.
	NodeID string `json:"node_id,omitempty"`
	Label  string `json:"label,omitempty"`
	Path   string `json:"path,omitempty"`
	Text   string `json:"text,omitempty"`
	// ExistedInPrev distinguishes "this revision broke it" from "it was already
	// dangling", which is the distinction that decides severity.
	ExistedInPrev bool   `json:"existed_in_prev"`
	Error         string `json:"error,omitempty"`
}

func (s *Set) resolveReference(_ context.Context, in ResolveReferenceIn) (ResolveReferenceOut, error) {
	refs := docmodel.ExtractReferences(0, in.Text, 0)
	if len(refs) == 0 {
		return ResolveReferenceOut{Error: fmt.Sprintf("tidak mengenali %q sebagai sitiran", in.Text)}, nil
	}
	ref := refs[0]

	out := ResolveReferenceOut{}
	if s.prev != nil {
		if _, ok := rules.Resolve(s.prev, ref); ok {
			out.ExistedInPrev = true
		}
	}
	n, ok := rules.Resolve(s.curr, ref)
	if !ok || n == nil {
		return out, nil
	}
	out.Found = true
	out.NodeID = n.ID
	out.Label = n.Label
	out.Path = n.Path()
	out.Text = clip(nodeText(s.curr, n), 600)
	return out, nil
}

// --- find_references_to ----------------------------------------------------

type FindReferencesToIn struct {
	NodeID string `json:"node_id" jsonschema:"description=structural node ID, e.g. \"pasal:12\""`
}

type Citation struct {
	ParagraphIndex int    `json:"paragraph_index"`
	Raw            string `json:"raw"`
	Text           string `json:"text"`
}

type FindReferencesToOut struct {
	Citations []Citation `json:"citations"`
	Count     int        `json:"count"`
}

// findReferencesTo answers "what breaks if this section goes away" — the
// question behind the product's core wedge. Word cannot answer it at all.
func (s *Set) findReferencesTo(_ context.Context, in FindReferencesToIn) (FindReferencesToOut, error) {
	out := FindReferencesToOut{}
	if s.curr == nil {
		return out, nil
	}
	for _, r := range s.curr.References {
		if r.TargetID() != in.NodeID {
			continue
		}
		text := ""
		if r.ParaIndex >= 0 && r.ParaIndex < len(s.curr.Paragraphs) {
			text = clip(s.curr.Paragraphs[r.ParaIndex].Text, 300)
		}
		out.Citations = append(out.Citations, Citation{
			ParagraphIndex: r.ParaIndex, Raw: r.Raw, Text: text,
		})
	}
	out.Count = len(out.Citations)
	return out, nil
}

// --- compare_paragraph -----------------------------------------------------

type ComparePargraphIn struct {
	PrevIndex int `json:"prev_index" jsonschema:"description=paragraph index in the OLD document"`
}

type ComparePargraphOut struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// compareParagraph is the only tool that reads the old document. Analysis
// otherwise works from the new version plus the diff summary; this exists for
// the case where the model needs the original wording of something the diff
// only quoted in part.
func (s *Set) compareParagraph(_ context.Context, in ComparePargraphIn) (ComparePargraphOut, error) {
	if s.prev == nil || in.PrevIndex < 0 || in.PrevIndex >= len(s.prev.Paragraphs) {
		return ComparePargraphOut{Error: "indeks di luar jangkauan dokumen lama"}, nil
	}
	return ComparePargraphOut{Text: s.prev.Paragraphs[in.PrevIndex].Text}, nil
}

// Tools returns the set as a map keyed by tool name, ready for a model request.
//
// Names and descriptions are fixed strings: the tool block is part of the
// cached prompt prefix, so anything varying per job here would silently
// invalidate the cache on every request.
func (s *Set) Tools() map[string]tool.Tool {
	return map[string]tool.Tool{
		"get_paragraph": function.NewFunctionTool(
			s.getParagraph,
			function.WithName("get_paragraph"),
			function.WithDescription(
				"Ambil isi paragraf tertentu dari dokumen versi baru beserta tetangganya. "+
					"Gunakan untuk memeriksa konteks kalimat sebelum menilai makna perubahan."),
		),
		"get_section_tree": function.NewFunctionTool(
			s.getSectionTree,
			function.WithName("get_section_tree"),
			function.WithDescription(
				"Lihat struktur hierarki (BAB/Pasal/ayat/huruf) dokumen versi baru. "+
					"Kosongkan node_id untuk tingkat teratas."),
		),
		"resolve_reference": function.NewFunctionTool(
			s.resolveReference,
			function.WithName("resolve_reference"),
			function.WithDescription(
				"Resolusikan sitiran seperti \"Pasal 12 ayat (3)\" ke node nyata di dokumen baru, "+
					"dan laporkan apakah target itu ada di versi lama."),
		),
		"find_references_to": function.NewFunctionTool(
			s.findReferencesTo,
			function.WithName("find_references_to"),
			function.WithDescription(
				"Cari semua paragraf yang merujuk ke satu node struktur. "+
					"Gunakan untuk menilai dampak penghapusan atau pemindahan pasal."),
		),
		"compare_paragraph": function.NewFunctionTool(
			s.compareParagraph,
			function.WithName("compare_paragraph"),
			function.WithDescription(
				"Ambil isi paragraf dari dokumen versi LAMA, untuk membandingkan redaksi asli."),
		),
	}
}

func ownerOf(doc *docmodel.IndexedDoc, paraIndex int) *docmodel.Node {
	if doc == nil || doc.Root == nil {
		return nil
	}
	var best *docmodel.Node
	doc.Root.Walk(func(n *docmodel.Node) {
		if n.Kind == docmodel.KindRoot || paraIndex < n.Start || paraIndex > n.End {
			return
		}
		if best == nil || n.Start >= best.Start {
			best = n
		}
	})
	return best
}

func nodeText(doc *docmodel.IndexedDoc, n *docmodel.Node) string {
	if doc == nil || n == nil {
		return ""
	}
	var b strings.Builder
	for i := n.Start; i <= n.End && i < len(doc.Paragraphs); i++ {
		if t := doc.Paragraphs[i].Text; t != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(t)
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
