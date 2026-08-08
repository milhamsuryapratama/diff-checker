package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// Some documents carry the same clauses more than once, side by side: an
// Indonesian "PASAL 6" followed immediately by its English "ARTICLE 6", or a
// three-language tender document, or a contract that repeats every clause in a
// second script. Each run is internally consistent and every citation resolves,
// so neither the numbering validator nor the reference checker can see when an
// edit to one run leaves the others behind — a pair reading "PASAL 6" /
// "ARTICLE 7", or a run with one section more than its neighbours.
//
// Nothing here names a language or a keyword. Parallel runs are found by how
// the document is shaped — headings of the same structural level whose members
// alternate with each other — so the same code handles two languages, five, or
// none, and handles keywords this project has never seen. A document with a
// single run produces no findings at all.

// minParallelRun is the shortest run worth comparing. Two headings alternating
// once is a coincidence; four is a pattern.
const minParallelRun = 3

// alternationRatio is how much of a combined sequence must switch between runs
// before it counts as interleaved rather than as separate blocks.
//
// Blocks matter: a document with all Indonesian articles first and all English
// ones after is not necessarily parallel — it may simply have two different
// parts — and reporting a count mismatch there would be a guess. Alternating
// headings leave no such doubt.
const alternationRatio = 0.6

// CheckParallelSequences reports runs of same-level headings that should track
// each other but do not.
func CheckParallelSequences(doc *docmodel.IndexedDoc) []docmodel.Finding {
	if doc == nil || doc.Root == nil {
		return nil
	}
	runs := parallelRuns(doc)
	if len(runs) < 2 {
		return nil
	}

	var out []docmodel.Finding

	// Count mismatch. The shortest run is treated as the intended length: an
	// unpaired trailing heading is an editing leftover far more often than it is
	// a clause that genuinely exists in one version only.
	shortest := len(runs[0].nodes)
	for _, r := range runs {
		if len(r.nodes) < shortest {
			shortest = len(r.nodes)
		}
	}
	for _, r := range runs {
		for _, extra := range r.nodes[shortest:] {
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatParallelMismatch,
				Severity:  docmodel.SeverityMajor,
				ParaIndex: docmodel.IntPtr(extra.HeadIndex),
				NodeID:    extra.ID,
				Message: fmt.Sprintf(
					"Jumlah judul antar-bagian tidak sama (%s). %q %s tidak punya pasangan",
					describeCounts(runs), r.keyword, extra.Number),
				Evidence: countEvidence(runs),
				Actions: []docmodel.Action{{
					Type:           docmodel.ActionDelete,
					ParagraphIndex: docmodel.IntPtr(extra.HeadIndex),
					Old:            extra.Label,
					Context:        extra.Path(),
					Rationale: fmt.Sprintf(
						"hapus agar setiap bagian punya jumlah judul yang sama (ikuti jumlah paling sedikit: %d)",
						shortest),
				}},
			})
		}
	}

	// Position-by-position mismatch across the paired runs.
	for i := 0; i < shortest; i++ {
		base := runs[0].nodes[i]
		for _, r := range runs[1:] {
			peer := r.nodes[i]
			if peer.Ordinal == base.Ordinal {
				continue
			}
			out = append(out, docmodel.Finding{
				Class:     docmodel.ClassVerified,
				Category:  docmodel.CatParallelMismatch,
				Severity:  docmodel.SeverityMajor,
				ParaIndex: docmodel.IntPtr(peer.HeadIndex),
				NodeID:    peer.ID,
				Message: fmt.Sprintf(
					"Penomoran antar-bagian tidak sinkron pada urutan ke-%d: %q %s berpasangan dengan %q %s",
					i+1, runs[0].keyword, base.Number, r.keyword, peer.Number),
				Evidence: []string{
					fmt.Sprintf("[%d] %s", base.HeadIndex, base.Label),
					fmt.Sprintf("[%d] %s", peer.HeadIndex, peer.Label),
				},
				// No action: the document-wide renumbering plan already assigns
				// 1..N to every run, which resynchronises them. A second fix
				// computed separately for the same paragraph is exactly the
				// contradiction this design set out to remove.
			})
		}
	}
	return out
}

type headingRun struct {
	keyword string
	nodes   []*docmodel.Node
}

// articleRuns groups every article-level heading by the word that introduces
// it, in document order.
//
// Grouping by keyword rather than by NodeKind is what makes multilingual
// documents work. A three-language contract using ARTIKEL, FASAL and ARTICLE
// parses all three as article-level nodes, and validating them as one sequence
// reads their perfectly correct 1,2,3 / 1,2,3 / 1,2,3 as a single run of
// 1,1,1,2,2,2,3,3,3 — nine duplicates and a pile of invented gaps. Each
// vocabulary is its own sequence and has to be numbered as one.
func articleRuns(doc *docmodel.IndexedDoc) []headingRun {
	byKeyword := map[string][]*docmodel.Node{}
	var order []string

	doc.Root.Walk(func(n *docmodel.Node) {
		if !n.Kind.IsArticleLevel() || n.HeadIndex < 0 {
			return
		}
		k := headingKeyword(n)
		if k == "" {
			return
		}
		if _, seen := byKeyword[k]; !seen {
			order = append(order, k)
		}
		byKeyword[k] = append(byKeyword[k], n)
	})

	runs := make([]headingRun, 0, len(order))
	for _, k := range order {
		nodes := byKeyword[k]
		sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].HeadIndex < nodes[j].HeadIndex })
		runs = append(runs, headingRun{keyword: k, nodes: nodes})
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].nodes[0].HeadIndex < runs[j].nodes[0].HeadIndex
	})
	return runs
}

// runLabel is the display name for a sequence, using the document's own word
// with its original capitalisation where available.
func runLabel(r headingRun) string {
	if len(r.nodes) > 0 {
		if fields := strings.Fields(r.nodes[0].Label); len(fields) > 0 {
			return strings.Trim(fields[0], ".:)-")
		}
	}
	return r.keyword
}

// parallelRuns groups article-level headings by the word that introduces them
// and keeps the groups only if they alternate through the document.
//
// The grouping key is whatever word the document itself uses — "Pasal",
// "Article", "Clause", "Artikel", "條", anything — because it is read off the
// heading rather than matched against a list.
func parallelRuns(doc *docmodel.IndexedDoc) []headingRun {
	var runs []headingRun
	for _, r := range articleRuns(doc) {
		if len(r.nodes) >= minParallelRun {
			runs = append(runs, r)
		}
	}
	if len(runs) < 2 || !interleaved(runs) {
		return nil
	}
	return runs
}

// headingKeyword is the introducing word of a heading, lowercased: "Pasal 12"
// yields "pasal". It is the document's own vocabulary, not ours.
func headingKeyword(n *docmodel.Node) string {
	label := strings.TrimSpace(n.Label)
	if label == "" {
		return ""
	}
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(fields[0], ".:)-"))
}

// interleaved reports whether the runs alternate through the document rather
// than sitting in separate blocks.
func interleaved(runs []headingRun) bool {
	type entry struct {
		pos int
		key string
	}
	var all []entry
	for _, r := range runs {
		for _, n := range r.nodes {
			all = append(all, entry{pos: n.HeadIndex, key: r.keyword})
		}
	}
	if len(all) < 2*minParallelRun {
		return false
	}
	sort.Slice(all, func(i, j int) bool { return all[i].pos < all[j].pos })

	switches := 0
	for i := 1; i < len(all); i++ {
		if all[i].key != all[i-1].key {
			switches++
		}
	}
	return float64(switches) >= alternationRatio*float64(len(all)-1)
}

func describeCounts(runs []headingRun) string {
	parts := make([]string, 0, len(runs))
	for _, r := range runs {
		parts = append(parts, fmt.Sprintf("%q punya %d", r.keyword, len(r.nodes)))
	}
	return strings.Join(parts, " dan ")
}

func countEvidence(runs []headingRun) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		out = append(out, fmt.Sprintf("%q: %d judul", r.keyword, len(r.nodes)))
	}
	return out
}
