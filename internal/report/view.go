package report

import (
	"fmt"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// ChangeView is one text change with everything the reviewer needs about it in
// one place: what changed, and what to do about it.
//
// The report used to list changes and findings as two separate sections, which
// left the reader correlating paragraph indexes by hand — the recommended fix
// for a change sat several screens away from the change itself. Findings are
// therefore attached to the change they concern, and only findings that belong
// to no change at all are listed separately.
type ChangeView struct {
	Change   docmodel.Change
	Title    string
	Findings []docmodel.Finding
}

// HasActions reports whether any attached finding proposes a concrete fix.
func (c ChangeView) HasActions() bool {
	for _, f := range c.Findings {
		if len(f.Actions) > 0 {
			return true
		}
	}
	return false
}

// View is the report reorganised for display.
type View struct {
	Changes []ChangeView

	// Renumbering is the document-wide numbering plan, one line per sequence.
	// It answers the question no single change can: after every proposed edit,
	// does the document actually run 1, 2, 3?
	Renumbering []string

	// PlanWarnings lists defects that survive applying the plan. Non-empty
	// means the plan is not a complete fix and must not be presented as one.
	PlanWarnings []string

	// Warnings are findings that carry no action by design — a reference that
	// was already dangling before this revision, a section removed wholesale.
	// They are shown so nothing is hidden, but they are not asks.
	Warnings []docmodel.Finding
}

// warningOnly reports whether a finding is informational by nature.
//
// Broken references are the clearest case: the engine can prove a citation no
// longer resolves, but choosing between deleting the sentence and repointing it
// is a drafting decision that depends on intent the document does not carry.
// Proposing an edit there would be guessing, so it is reported and left alone.
func warningOnly(f docmodel.Finding) bool {
	switch f.Category {
	case docmodel.CatBrokenReference, docmodel.CatShiftedReference,
		docmodel.CatUnresolvedRelated, docmodel.CatSectionRemoved,
		docmodel.CatSectionAdded, docmodel.CatSectionRenamed:
		return true
	}
	return false
}

// BuildView groups findings under the changes they describe.
//
// Matching is by paragraph index first, then by structural node. A finding is
// attached to at most one change, so nothing is shown twice and nothing is
// silently lost: whatever fails to match ends up in Orphans.
func BuildView(r *docmodel.Report) View {
	var v View
	if r == nil {
		return v
	}

	// Index every paragraph each change covers, on both sides, so a finding
	// about a deletion (which can only be located in the old document) still
	// finds its change.
	currIdx := map[int]int{} // paragraph -> position in r.Changes
	prevIdx := map[int]int{}
	nodeIdx := map[string]int{}
	for i, c := range r.Changes {
		for _, p := range c.CurrIndexes {
			currIdx[p] = i
		}
		for _, p := range c.PrevIndexes {
			prevIdx[p] = i
		}
		if c.NodeID != "" {
			if _, seen := nodeIdx[c.NodeID]; !seen {
				nodeIdx[c.NodeID] = i
			}
		}
	}

	// Article-level index, used as a fallback. Deleting a paragraph shifts every
	// index after it, so a finding *caused* by an edit rarely shares a
	// paragraph number with it: removing "ayat (2)" from Pasal 3 is recorded
	// against the old document, while the numbering gap it opens is recorded
	// against "ayat (3)" in the new one. The two are obviously the same story
	// to a reader, and the article they share is what says so.
	articleIdx := map[string][]int{}
	for i, c := range r.Changes {
		if a := articleOf(c.NodeID); a != "" {
			articleIdx[a] = append(articleIdx[a], i)
		}
	}

	views := make([]ChangeView, len(r.Changes))
	for i, c := range r.Changes {
		views[i] = ChangeView{Change: c, Title: DescribeChange(c)}
	}

	v.Renumbering = r.Renumbering
	v.PlanWarnings = r.RenumberingWarnings

	for _, f := range r.Findings {
		// A finding that proposes no fix and never could is a warning, not an
		// item on a to-do list; keeping the two apart is what stops the report
		// reading as a pile of unactionable noise.
		if warningOnly(f) && len(f.Actions) == 0 {
			v.Warnings = append(v.Warnings, f)
			continue
		}
		if i, ok := matchChange(f, currIdx, prevIdx, nodeIdx); ok {
			views[i].Findings = append(views[i].Findings, f)
			continue
		}
		// Only attach by article when it is unambiguous. With several edits in
		// one article there is no way to tell which one a finding belongs to,
		// and filing a fix under the wrong edit is worse than filing it under
		// the numbering plan, which covers the document as a whole.
		if a := articleOf(f.NodeID); a != "" {
			if hits := articleIdx[a]; len(hits) == 1 {
				views[hits[0]].Findings = append(views[hits[0]].Findings, f)
				continue
			}
		}
		// Everything left concerns the document's numbering as a whole rather
		// than one edit, and the plan above is its fix.
		v.Warnings = append(v.Warnings, f)
	}

	v.Changes = views
	return v
}

// articleOf reduces a node ID to the article that contains it, so
// "pasal:3/ayat:2" and "pasal:3/ayat:3" are recognised as the same article.
// Node IDs above article level ("bab:IV") have no article and return empty.
func articleOf(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	head := nodeID
	if i := strings.Index(nodeID, "/"); i >= 0 {
		head = nodeID[:i]
	}
	switch {
	case strings.HasPrefix(head, "pasal:"), strings.HasPrefix(head, "article:"),
		strings.HasPrefix(head, "clause:"):
		return head
	}
	// A nested path may still reach an article deeper in ("root/huruf:a").
	for _, part := range strings.Split(nodeID, "/") {
		if strings.HasPrefix(part, "pasal:") || strings.HasPrefix(part, "article:") {
			return part
		}
	}
	return ""
}

func matchChange(f docmodel.Finding, currIdx, prevIdx map[int]int, nodeIdx map[string]int) (int, bool) {
	if f.ParaIndex != nil {
		side := f.Side.Resolved()
		if side == docmodel.SidePrev {
			if i, ok := prevIdx[*f.ParaIndex]; ok {
				return i, true
			}
		} else if i, ok := currIdx[*f.ParaIndex]; ok {
			return i, true
		}
	}
	if f.NodeID != "" {
		if i, ok := nodeIdx[f.NodeID]; ok {
			return i, true
		}
	}
	return 0, false
}

// DescribeChange writes a title that says what actually changed.
//
// "MODIFIED — Pasal 2" told the reader only where to look. The useful headline
// is the edit itself: a renumbered heading, a deleted clause, a reworded term.
// The location is still shown, but as context rather than as the whole title.
func DescribeChange(c docmodel.Change) string {
	where := c.Context
	prev, curr := strings.TrimSpace(c.PrevText), strings.TrimSpace(c.CurrText)

	switch {
	case curr == "" && prev != "":
		// The diff calls this "modified" when a paragraph is emptied rather
		// than removed outright, but to a reader it is a deletion. The snippet
		// matters here: several clauses inside one article can be deleted
		// separately, and without it every one of them reads "Teks dihapus
		// pada Pasal 6".
		return fmt.Sprintf("Teks dihapus%s: %q", suffix(where), snippet(prev))
	case prev == "" && curr != "":
		return fmt.Sprintf("Teks ditambahkan%s: %q", suffix(where), snippet(curr))
	}

	switch c.Type {
	case docmodel.ChangeAdded:
		return fmt.Sprintf("Bagian baru ditambahkan%s", suffix(where))
	case docmodel.ChangeRemoved:
		return fmt.Sprintf("Bagian dihapus%s", suffix(where))
	}

	// A heading whose only difference is its number is a renumbering, and
	// saying so is far more useful than "modified".
	if a, b, ok := headingRenumber(prev, curr); ok {
		return fmt.Sprintf("Penomoran diubah: %s → %s", a, b)
	}
	if c.Cosmetic {
		return fmt.Sprintf("Perubahan kosmetik%s", suffix(where))
	}
	return fmt.Sprintf("Teks diubah%s", suffix(where))
}

// snippet shortens text to a title-sized quote.
func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= 48 {
		return s
	}
	return strings.TrimSpace(string(r[:48])) + "…"
}

func suffix(where string) string {
	if where == "" {
		return ""
	}
	return " pada " + where
}

// headingKeywords are the words a structural heading starts with. Requiring one
// is what keeps this from firing on ordinary prose: "jangka waktu 30 hari" ->
// "jangka waktu 60 hari" also differs by a single numeric token, and calling
// that a renumbering would mislabel the most important kind of edit there is —
// a changed contractual term — as a formatting detail.
var headingKeywords = []string{
	"pasal", "article", "bab", "bagian", "paragraf", "clause", "section", "chapter",
}

// headingRenumber detects a pair like "PASAL 5" / "PASAL 8" — same heading, one
// differing number.
func headingRenumber(prev, curr string) (string, string, bool) {
	if prev == "" || curr == "" || prev == curr {
		return "", "", false
	}
	pf, cf := strings.Fields(prev), strings.Fields(curr)
	if len(pf) != len(cf) || len(pf) == 0 || len(pf) > 4 {
		return "", "", false
	}
	if !isHeadingWord(pf[0]) || !isHeadingWord(cf[0]) {
		return "", "", false
	}
	diffs := 0
	for i := range pf {
		if pf[i] != cf[i] {
			diffs++
			if !isNumberish(pf[i]) || !isNumberish(cf[i]) {
				return "", "", false
			}
		}
	}
	if diffs != 1 {
		return "", "", false
	}
	return prev, curr, true
}

func isHeadingWord(s string) bool {
	s = strings.ToLower(strings.Trim(s, ".:)-"))
	for _, k := range headingKeywords {
		if s == k {
			return true
		}
	}
	return false
}

// isNumberish accepts arabic and roman numerals, with optional trailing
// punctuation, which is what a heading number looks like.
func isNumberish(s string) bool {
	s = strings.Trim(s, ".:)-")
	if s == "" {
		return false
	}
	digits, romans := true, true
	for _, r := range s {
		if r < '0' || r > '9' {
			digits = false
		}
		if !strings.ContainsRune("IVXLCDMivxlcdm", r) {
			romans = false
		}
	}
	return digits || romans
}
