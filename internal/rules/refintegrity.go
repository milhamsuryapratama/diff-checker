package rules

import (
	"fmt"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
)

// relocationThreshold is the content-similarity level above which a missing
// target is treated as relocated rather than deleted. Chosen conservatively:
// below it the engine says "target is gone" and leaves the decision to a human
// or to the LLM tier rather than proposing a repoint that might be wrong.
const relocationThreshold = 0.75

// CheckReferences validates every cross-reference against a single document.
//
// A reference is broken when its target node does not exist. This is the
// deterministic core of the check: it does not depend on a model remembering a
// 25,000-token document, only on resolving a citation against a parsed tree.
func CheckReferences(doc *docmodel.IndexedDoc) []docmodel.Finding {
	if doc == nil {
		return nil
	}
	var out []docmodel.Finding
	for _, r := range doc.References {
		if _, ok := resolve(doc, r); ok {
			continue
		}
		out = append(out, docmodel.Finding{
			Class:     docmodel.ClassVerified,
			Category:  docmodel.CatBrokenReference,
			Severity:  docmodel.SeverityMajor,
			ParaIndex: docmodel.IntPtr(r.ParaIndex),
			Reference: r.String(),
			Message:   fmt.Sprintf("Referensi ke %s tidak dapat ditemukan dalam dokumen", r.String()),
			Evidence:  []string{fmt.Sprintf("[%d] %s", r.ParaIndex, excerpt(doc, r))},
		})
	}
	return out
}

// CompareReferences checks reference integrity across a revision.
//
// This is the check that answers the question a reviewer actually has: not
// merely "is this reference broken" but "did *this revision* break it, and what
// happened to the target". A reference whose target existed in the previous
// version and is gone from the current one is a regression introduced by the
// edit under review, and is reported far more seriously than a citation that was
// already dangling before anyone touched the document.
//
// When the vanished target's content reappears elsewhere — a Pasal that was
// merged or moved rather than deleted — the relocation is detected by content
// similarity and a concrete repoint action is proposed. Below the similarity
// threshold the engine reports the deletion and stops, rather than guessing.
func CompareReferences(prev, curr *docmodel.IndexedDoc) []docmodel.Finding {
	if curr == nil {
		return nil
	}
	if prev == nil {
		return CheckReferences(curr)
	}

	var out []docmodel.Finding
	// Cache relocation lookups: many paragraphs cite the same vanished target.
	relocations := map[string]*docmodel.Node{}

	for _, r := range curr.References {
		if _, ok := resolve(curr, r); ok {
			continue
		}

		targetID := r.TargetID()
		existedBefore := false
		if targetID != "" {
			if _, ok := resolve(prev, r); ok {
				existedBefore = true
			}
		}

		if !existedBefore {
			// Already dangling before this revision. Still worth reporting, but it
			// is not a regression and should not dominate the review.
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatBrokenReference,
				Severity:  docmodel.SeverityMinor,
				ParaIndex: docmodel.IntPtr(r.ParaIndex),
				Reference: r.String(),
				Message: fmt.Sprintf("Referensi ke %s tidak dapat ditemukan (sudah bermasalah sejak versi sebelumnya)",
					r.String()),
				Evidence: []string{fmt.Sprintf("[%d] %s", r.ParaIndex, excerpt(curr, r))},
			})
			continue
		}

		// The target existed and is now gone: this revision broke it.
		reloc, cached := relocations[targetID]
		if !cached {
			reloc = findRelocation(prev, curr, targetID)
			relocations[targetID] = reloc
		}

		f := docmodel.Finding{
			Class:     docmodel.ClassVerified,
			Category:  docmodel.CatBrokenReference,
			Severity:  docmodel.SeverityCritical,
			ParaIndex: docmodel.IntPtr(r.ParaIndex),
			Reference: r.String(),
			Evidence:  []string{fmt.Sprintf("[%d] %s", r.ParaIndex, excerpt(curr, r))},
		}

		if reloc != nil {
			f.Category = docmodel.CatShiftedReference
			f.NodeID = reloc.ID
			f.Message = fmt.Sprintf(
				"Paragraf [%d] merujuk ke %s, yang sudah tidak ada di versi baru. Isinya tampak dipindahkan ke %s",
				r.ParaIndex, r.String(), reloc.Path())
			if act, ok := repointAction(curr, r, reloc); ok {
				f.Actions = append(f.Actions, act)
			}
		} else {
			f.Message = fmt.Sprintf(
				"Paragraf [%d] merujuk ke %s, yang dihapus di versi baru — referensi menjadi menggantung",
				r.ParaIndex, r.String())
			f.Actions = append(f.Actions, docmodel.Action{
				Type:           docmodel.ActionManualReview,
				ParagraphIndex: docmodel.IntPtr(r.ParaIndex),
				Old:            r.Raw,
				Context:        contextOf(curr, r.ParaIndex),
				Rationale: "target referensi dihapus dan isinya tidak ditemukan di tempat lain; " +
					"putuskan apakah referensi dihapus atau diarahkan ulang",
			})
		}
		out = append(out, f)
	}

	// Targets that disappeared but were never cited are reported as structural
	// deletions, so a reviewer still sees that an article vanished.
	out = append(out, reportRemovedArticles(prev, curr)...)
	return out
}

// resolve looks a reference's target up in a document.
func resolve(doc *docmodel.IndexedDoc, r docmodel.Reference) (*docmodel.Node, bool) {
	if doc == nil || r.Pasal == "" {
		return nil, false
	}
	if id := r.TargetID(); id != "" {
		if n, ok := doc.Node(id); ok {
			return n, true
		}
	}
	// Fall back to the citation walk, which reports the deepest level that did
	// resolve — useful when only the sub-level is missing.
	return docmodel.FindByCitation(doc.Nodes, r.Pasal, r.Ayat, r.Huruf, r.Angka)
}

// findRelocation looks for the vanished node's content elsewhere in the new
// document, which is how a "moved" article is told apart from a deleted one.
func findRelocation(prev, curr *docmodel.IndexedDoc, targetID string) *docmodel.Node {
	old, ok := prev.Node(targetID)
	if !ok {
		return nil
	}
	oldText := nodeText(prev, old)
	if len(strings.Fields(oldText)) < 5 {
		// Too little text to match on; a false repoint is worse than none.
		return nil
	}

	var best *docmodel.Node
	bestScore := relocationThreshold
	curr.Root.Walk(func(n *docmodel.Node) {
		if n.Kind != old.Kind || n.ID == targetID {
			return
		}
		if s := similarity(oldText, nodeText(curr, n)); s > bestScore {
			best, bestScore = n, s
		}
	})
	return best
}

// repointAction rewrites the citation to name the relocated target.
func repointAction(curr *docmodel.IndexedDoc, r docmodel.Reference, reloc *docmodel.Node) (docmodel.Action, bool) {
	if r.Raw == "" || reloc.Number == "" {
		return docmodel.Action{}, false
	}
	// Substitute only the article number inside the citation, preserving the
	// sub-levels and the surrounding wording exactly as written.
	updated := strings.Replace(r.Raw, r.Pasal, reloc.Number, 1)
	if updated == r.Raw {
		return docmodel.Action{}, false
	}
	return docmodel.Action{
		Type:           docmodel.ActionReferenceUpdate,
		ParagraphIndex: docmodel.IntPtr(r.ParaIndex),
		Old:            r.Raw,
		New:            updated,
		Context:        contextOf(curr, r.ParaIndex),
		Rationale:      fmt.Sprintf("isi target pindah ke %s", reloc.Path()),
	}, true
}

// reportRemovedArticles surfaces article-level deletions even when nothing cited
// them, since an article disappearing is itself a reviewable event.
func reportRemovedArticles(prev, curr *docmodel.IndexedDoc) []docmodel.Finding {
	var out []docmodel.Finding
	prev.Root.Walk(func(n *docmodel.Node) {
		if !n.Kind.IsArticleLevel() {
			return
		}
		if _, still := curr.Node(n.ID); still {
			return
		}
		f := docmodel.Finding{
			Class:    docmodel.ClassVerified,
			Category: docmodel.CatSectionRemoved,
			Severity: docmodel.SeverityMajor,
			NodeID:   n.ID,
			// The heading only exists in the previous version, so the index must
			// be read against that document.
			ParaIndex: docmodel.IntPtr(n.HeadIndex),
			Side:      docmodel.SidePrev,
			Message:   fmt.Sprintf("%s tidak lagi ada di versi baru", n.Path()),
		}
		if reloc := findRelocation(prev, curr, n.ID); reloc != nil {
			f.Severity = docmodel.SeverityMinor
			f.Message = fmt.Sprintf("%s tidak lagi ada di versi baru; isinya tampak menjadi %s",
				n.Path(), reloc.Path())
		}
		out = append(out, f)
	})
	return out
}

// nodeText concatenates the normalized text a node covers.
func nodeText(doc *docmodel.IndexedDoc, n *docmodel.Node) string {
	if doc == nil || n == nil {
		return ""
	}
	var b strings.Builder
	for i := n.Start; i <= n.End && i < len(doc.Paragraphs); i++ {
		if i < 0 {
			continue
		}
		if t := doc.Paragraphs[i].Text; t != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(t)
		}
	}
	return b.String()
}

// similarity is token-set Jaccard over aggressively normalized text: case,
// punctuation and marker noise removed. It answers "is this the same clause,
// possibly renumbered", which is exactly the relocation question.
func similarity(a, b string) float64 {
	ta := tokenSet(a)
	tb := tokenSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for tok := range ta {
		if tb[tok] {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(ingest.NormalizeAggressive(s)) {
		out[w] = true
	}
	return out
}

// contextOf labels a paragraph with its structural citation for display.
func contextOf(doc *docmodel.IndexedDoc, paraIndex int) string {
	if owner := ownerOf(doc, paraIndex); owner != nil {
		return owner.Path()
	}
	return ""
}

// ownerOf finds the innermost node covering a paragraph. Duplicated from the
// structure package to keep rules free of an import cycle.
func ownerOf(doc *docmodel.IndexedDoc, paraIndex int) *docmodel.Node {
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

// excerpt renders a short quote around a reference for evidence.
func excerpt(doc *docmodel.IndexedDoc, r docmodel.Reference) string {
	p, ok := doc.Para(r.ParaIndex)
	if !ok {
		return r.Raw
	}
	const window = 60
	start := r.Start - window
	if start < 0 {
		start = 0
	}
	end := r.End + window
	if end > len(p.Text) {
		end = len(p.Text)
	}
	s := p.Text[start:end]
	if start > 0 {
		s = "..." + s
	}
	if end < len(p.Text) {
		s += "..."
	}
	return s
}
