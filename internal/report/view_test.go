package report

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

func TestDescribeChange(t *testing.T) {
	tests := []struct {
		name string
		in   docmodel.Change
		want string
	}{
		{
			name: "heading renumber is named as such",
			in: docmodel.Change{
				Type: docmodel.ChangeModified, PrevText: "PASAL 5", CurrText: "PASAL 8",
			},
			want: "Penomoran diubah: PASAL 5 → PASAL 8",
		},
		{
			name: "roman heading renumber",
			in: docmodel.Change{
				Type: docmodel.ChangeModified, PrevText: "BAB IV", CurrText: "BAB VI",
			},
			want: "Penomoran diubah: BAB IV → BAB VI",
		},
		{
			name: "emptied paragraph reads as a deletion, with a quote",
			in: docmodel.Change{
				Type: docmodel.ChangeModified, Context: "Pasal 6",
				PrevText: "ARTICLE 6 TERM",
			},
			want: `Teks dihapus pada Pasal 6: "ARTICLE 6 TERM"`,
		},
		{
			name: "ordinary edit names its location",
			in: docmodel.Change{
				Type: docmodel.ChangeModified, Context: "Pasal 4 ayat (1)",
				PrevText: "jangka waktu 30 hari", CurrText: "jangka waktu 60 hari",
			},
			want: "Teks diubah pada Pasal 4 ayat (1)",
		},
		{
			name: "a reworded heading is not a renumbering",
			in: docmodel.Change{
				Type: docmodel.ChangeModified, Context: "Pasal 2",
				PrevText: "PASAL 2 DEFINISI", CurrText: "PASAL 2 KETENTUAN",
			},
			want: "Teks diubah pada Pasal 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DescribeChange(tc.in); got != tc.want {
				t.Errorf("DescribeChange() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two deletions inside one article must not share a title, or the reader
// cannot tell which block a finding belongs to.
func TestDeletionTitlesStayDistinct(t *testing.T) {
	a := DescribeChange(docmodel.Change{Context: "Pasal 6", PrevText: "ARTICLE 6 TERM"})
	b := DescribeChange(docmodel.Change{
		Context: "Pasal 6", PrevText: "This Agreement is effective for 1-2 years.",
	})
	if a == b {
		t.Errorf("two different deletions produced the same title: %q", a)
	}
}

// A finding located exactly on a change's paragraph belongs to that change.
func TestFindingAttachesByParagraph(t *testing.T) {
	r := &docmodel.Report{
		Changes: []docmodel.Change{{ID: 1, CurrIndexes: []int{20}, NodeID: "pasal:4/ayat:1"}},
		Findings: []docmodel.Finding{{
			Category: docmodel.CatNumberingGap, ParaIndex: docmodel.IntPtr(20),
			Actions: []docmodel.Action{{Type: docmodel.ActionRenumber}},
		}},
	}
	v := BuildView(r)

	if len(v.Warnings) != 0 {
		t.Fatalf("finding not attached to its change: %+v", v.Warnings)
	}
	if len(v.Changes[0].Findings) != 1 || !v.Changes[0].HasActions() {
		t.Errorf("finding or its action not attached: %+v", v.Changes[0])
	}
}

// Deleting a paragraph shifts later indexes, so a consequential finding shares
// only the article with the edit that caused it.
func TestFindingAttachesByArticleWhenIndexesShift(t *testing.T) {
	r := &docmodel.Report{
		Changes: []docmodel.Change{{ID: 1, PrevIndexes: []int{14}, NodeID: "pasal:3/ayat:2"}},
		Findings: []docmodel.Finding{{
			Category:  docmodel.CatNumberingGap,
			ParaIndex: docmodel.IntPtr(14), Side: docmodel.SideCurr,
			NodeID: "pasal:3/ayat:3",
		}},
	}
	v := BuildView(r)

	if len(v.Changes[0].Findings) != 1 {
		t.Errorf("finding in the same article was not attached: warnings=%d", len(v.Warnings))
	}
}

// With two edits in one article there is no way to know which one a finding
// belongs to; guessing would file a fix under the wrong edit.
func TestAmbiguousArticleLeavesFindingUnattached(t *testing.T) {
	r := &docmodel.Report{
		Changes: []docmodel.Change{
			{ID: 1, PrevIndexes: []int{14}, NodeID: "pasal:3/ayat:2"},
			{ID: 2, PrevIndexes: []int{15}, NodeID: "pasal:3/ayat:4"},
		},
		Findings: []docmodel.Finding{{
			Category: docmodel.CatNumberingGap, NodeID: "pasal:3/ayat:9",
		}},
	}
	v := BuildView(r)

	if len(v.Warnings) != 1 {
		t.Errorf("ambiguous finding was attached anyway: %+v", v.Changes)
	}
}

// Nothing may be silently dropped: every finding is either attached or listed.
func TestEveryFindingIsAccountedFor(t *testing.T) {
	r := &docmodel.Report{
		Changes: []docmodel.Change{{ID: 1, CurrIndexes: []int{5}, NodeID: "pasal:1"}},
		Findings: []docmodel.Finding{
			{Category: docmodel.CatNumberingGap, ParaIndex: docmodel.IntPtr(5)},
			{Category: docmodel.CatBrokenReference, ParaIndex: docmodel.IntPtr(900)},
			{Category: docmodel.CatNumberingDuplicate},
		},
	}
	v := BuildView(r)

	total := len(v.Warnings)
	for _, c := range v.Changes {
		total += len(c.Findings)
	}
	if total != len(r.Findings) {
		t.Errorf("accounted for %d of %d findings", total, len(r.Findings))
	}
}

func TestArticleOf(t *testing.T) {
	tests := map[string]string{
		"pasal:3/ayat:2":       "pasal:3",
		"article:8":            "article:8",
		"pasal:12":             "pasal:12",
		"bab:IV":               "",
		"root/huruf:PT#4":      "",
		"":                     "",
		"article:2/clause:2.3": "article:2",
	}
	for in, want := range tests {
		if got := articleOf(in); got != want {
			t.Errorf("articleOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildViewHandlesNilReport(t *testing.T) {
	v := BuildView(nil)
	if len(v.Changes) != 0 || len(v.Warnings) != 0 {
		t.Error("nil report should produce an empty view")
	}
}

func TestSnippetIsShortAndSingleLine(t *testing.T) {
	got := snippet("baris satu\n   baris  dua dengan banyak sekali kata sampai panjang sekali melebihi batas")
	if strings.Contains(got, "\n") {
		t.Errorf("snippet kept a newline: %q", got)
	}
	if len([]rune(got)) > 50 {
		t.Errorf("snippet too long (%d runes): %q", len([]rune(got)), got)
	}
}

// Broken references are reported, never fixed: choosing between deleting the
// sentence and repointing it needs intent the document does not carry.
func TestBrokenReferenceIsWarningWithoutAction(t *testing.T) {
	r := &docmodel.Report{
		Changes: []docmodel.Change{{ID: 1, CurrIndexes: []int{5}}},
		Findings: []docmodel.Finding{{
			Category:  docmodel.CatBrokenReference,
			ParaIndex: docmodel.IntPtr(5),
		}},
	}
	v := BuildView(r)

	if len(v.Warnings) != 1 {
		t.Fatalf("broken reference should be a warning, got %d", len(v.Warnings))
	}
	for _, c := range v.Changes {
		if c.HasActions() {
			t.Error("broken reference must not carry a proposed fix")
		}
	}
}
