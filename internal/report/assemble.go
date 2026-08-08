// Package report assembles the deterministic stages into a single Report and
// renders it.
//
// Everything this package produces today is docmodel.ClassVerified. The LLM
// tier appends ClassAdvisory findings to the same Report later, which is why the
// two classes are separate fields on every finding rather than separate reports:
// the UI shows one list, clearly labelled.
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/rules"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
	"github.com/milhamsuryapratama/diff-checker/internal/textdiff"
)

// Build runs the full deterministic pipeline over two parsed documents.
//
// The documents must already have been through structure.Build; Build calls it
// defensively so that callers cannot forget.
func Build(prev, curr *docmodel.IndexedDoc) *docmodel.Report {
	if prev == nil || curr == nil {
		return &docmodel.Report{}
	}
	if prev.Root == nil {
		structure.Build(prev)
	}
	if curr.Root == nil {
		structure.Build(curr)
	}

	r := &docmodel.Report{
		PrevSource:       prev.Source,
		CurrSource:       curr.Source,
		PrevArticleCount: structure.ArticleCount(prev),
		CurrArticleCount: structure.ArticleCount(curr),
	}

	r.Changes = textdiff.Compare(prev, curr)
	r.Summary = textdiff.Summarize(r.Changes)

	// Numbering is validated against the new document: that is the version being
	// approved, and a defect there is what ships.
	for _, f := range rules.ValidateNumbering(curr) {
		r.AddFinding(f)
	}
	// Reference integrity is validated across the revision, so that a citation
	// this edit broke is separated from one that was already dangling.
	for _, f := range rules.CompareReferences(prev, curr) {
		r.AddFinding(f)
	}

	docmodel.SortFindings(r.Findings)
	return r
}

// RenderText writes a human-readable report, used by the CLI.
func RenderText(w io.Writer, r *docmodel.Report, showChanges bool) {
	fmt.Fprintf(w, "Perbandingan dokumen\n")
	fmt.Fprintf(w, "  sebelum : %s\n", r.PrevSource)
	fmt.Fprintf(w, "  sesudah : %s\n\n", r.CurrSource)

	fmt.Fprintf(w, "Ringkasan perubahan\n")
	fmt.Fprintf(w, "  ditambah   : %d\n", r.Summary.Added)
	fmt.Fprintf(w, "  dihapus    : %d\n", r.Summary.Removed)
	fmt.Fprintf(w, "  diubah     : %d\n", r.Summary.Modified)
	fmt.Fprintf(w, "  total      : %d\n", r.Summary.Total)
	fmt.Fprintf(w, "  jumlah pasal: %d -> %d\n\n", r.PrevArticleCount, r.CurrArticleCount)

	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "Tidak ada temuan struktural.\n")
	} else {
		fmt.Fprintf(w, "Temuan (%d terverifikasi, %d saran AI)\n",
			r.Summary.Verified, r.Summary.Advisory)
		fmt.Fprintf(w, "  kritis: %d  mayor: %d  minor: %d  info: %d\n\n",
			r.Summary.Critical, r.Summary.Major, r.Summary.Minor, r.Summary.Info)

		for _, f := range r.Findings {
			fmt.Fprintf(w, "  [%s] %s  (%s)\n", strings.ToUpper(string(f.Severity)), f.Message, f.Category)
			fmt.Fprintf(w, "        kelas: %s", f.Class)
			if f.ParaIndex != nil {
				where := "versi baru"
				if f.Side.Resolved() == docmodel.SidePrev {
					where = "versi lama"
				}
				fmt.Fprintf(w, "   paragraf: [%d] (%s)", *f.ParaIndex, where)
			}
			if f.NodeID != "" {
				fmt.Fprintf(w, "   node: %s", f.NodeID)
			}
			fmt.Fprintln(w)
			for _, e := range f.Evidence {
				fmt.Fprintf(w, "        bukti: %s\n", truncate(e, 110))
			}
			for _, a := range f.Actions {
				fmt.Fprintf(w, "        usul (%s): %s\n", a.Type, describeAction(a))
			}
			fmt.Fprintln(w)
		}
	}

	if !showChanges || len(r.Changes) == 0 {
		return
	}
	fmt.Fprintf(w, "\nDaftar perubahan teks\n\n")
	for _, c := range r.Changes {
		label := string(c.Type)
		if c.Cosmetic {
			label += " (kosmetik)"
		}
		fmt.Fprintf(w, "  #%d %s", c.ID, label)
		if c.Context != "" {
			fmt.Fprintf(w, " — %s", c.Context)
		}
		fmt.Fprintln(w)
		if c.PrevText != "" {
			fmt.Fprintf(w, "      - %s\n", truncate(c.PrevText, 140))
		}
		if c.CurrText != "" {
			fmt.Fprintf(w, "      + %s\n", truncate(c.CurrText, 140))
		}
		fmt.Fprintln(w)
	}
}

func describeAction(a docmodel.Action) string {
	var b strings.Builder
	if a.ParagraphIndex != nil {
		fmt.Fprintf(&b, "[%d] ", *a.ParagraphIndex)
	}
	switch a.Type {
	case docmodel.ActionManualReview:
		b.WriteString(a.Rationale)
	default:
		fmt.Fprintf(&b, "%q -> %q", a.Old, a.New)
		if a.Rationale != "" {
			fmt.Fprintf(&b, "  (%s)", a.Rationale)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// ExitCode maps a report onto a process exit status so the CLI can be used as a
// CI gate: 0 clean, 1 findings that need attention, 2 critical findings.
func ExitCode(r *docmodel.Report) int {
	switch {
	case r.Summary.Critical > 0:
		return 2
	case r.Summary.Major > 0:
		return 1
	}
	return 0
}

// FindingsByCategory groups findings for the UI, preserving severity order.
func FindingsByCategory(fs []docmodel.Finding) map[docmodel.Category][]docmodel.Finding {
	out := map[docmodel.Category][]docmodel.Finding{}
	for _, f := range fs {
		out[f.Category] = append(out[f.Category], f)
	}
	for k := range out {
		g := out[k]
		sort.SliceStable(g, func(i, j int) bool {
			a, b := -1, -1
			if g[i].ParaIndex != nil {
				a = *g[i].ParaIndex
			}
			if g[j].ParaIndex != nil {
				b = *g[j].ParaIndex
			}
			return a < b
		})
	}
	return out
}
