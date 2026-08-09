package docmodel

import (
	"fmt"
	"sort"
)

// Class separates what the engine proved from what a model suggested.
//
// This distinction is load-bearing for the product, not cosmetic: legal reviewers
// will not act on output they cannot tell apart from a guess. Deterministic
// findings never pass through an LLM, so they cannot be degraded by one.
type Class string

const (
	// ClassVerified is produced by deterministic Go analysis. Reproducible.
	ClassVerified Class = "verified"
	// ClassAdvisory is produced by an LLM. Presented explicitly as a suggestion.
	ClassAdvisory Class = "advisory"
)

// Side identifies which of the two compared documents an index refers to.
type Side string

const (
	// SideCurr is the new version under review; the default.
	SideCurr Side = "curr"
	// SidePrev is the previous version, used for findings about deletions.
	SidePrev Side = "prev"
)

// Resolved returns the side, defaulting to SideCurr when unset.
func (s Side) Resolved() Side {
	if s == SidePrev {
		return SidePrev
	}
	return SideCurr
}

// Severity ranks how much a finding matters.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
	SeverityInfo     Severity = "info"
)

// severityRank orders severities for sorting, highest first.
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityMajor:
		return 1
	case SeverityMinor:
		return 2
	case SeverityInfo:
		return 3
	}
	return 4
}

// Category is the machine-readable kind of a finding.
type Category string

const (
	// Numbering integrity — all deterministic.
	CatNumberingGap        Category = "numbering_gap"
	CatNumberingDuplicate  Category = "numbering_duplicate"
	CatNumberingOutOfOrder Category = "numbering_out_of_order"
	CatNumberingBadStart   Category = "numbering_bad_start"
	CatNumberingMixedForm  Category = "numbering_mixed_form"

	// Reference integrity — all deterministic.
	CatBrokenReference   Category = "broken_reference"
	CatShiftedReference  Category = "shifted_reference"
	CatUnresolvedRelated Category = "unresolvable_relative_reference"

	// Parallel-run integrity — deterministic. A document that repeats its
	// clauses in more than one language or script keeps those runs in lockstep;
	// an edit to one run alone leaves a mismatch that no single run can detect
	// from the inside.
	CatParallelMismatch Category = "parallel_mismatch"

	// Structural deltas — deterministic.
	CatSectionAdded   Category = "section_added"
	CatSectionRemoved Category = "section_removed"
	CatSectionRenamed Category = "section_renamed"

	// Semantic — advisory, produced by the LLM tier.
	CatMeaningChange Category = "meaning_change"
	CatLegalRisk     Category = "legal_risk"
)

// ActionType is the kind of fix an action performs.
type ActionType string

const (
	// ActionReplacement rewrites literal text inside one paragraph.
	ActionReplacement ActionType = "replacement"
	// ActionRenumber restarts or shifts a numbering sequence.
	ActionRenumber ActionType = "renumber"
	// ActionReferenceUpdate repoints a stale cross-reference.
	ActionReferenceUpdate ActionType = "reference_update"
	// ActionManualReview flags something a machine should not fix on its own.
	ActionManualReview ActionType = "manual_review"
	// ActionDelete removes a heading outright, used when one parallel run
	// carries a section its peers do not.
	ActionDelete ActionType = "delete"
)

// Action is a concrete, applicable fix.
//
// Every action must survive the grounding validator before it reaches a user:
// ParagraphIndex has to exist and Old has to appear verbatim in that paragraph.
// An action that cannot be located in the document is rejected, never displayed.
type Action struct {
	Type ActionType `json:"type"`

	// ParagraphIndex is the target paragraph in the current (new) document.
	ParagraphIndex *int `json:"paragraph_index,omitempty"`

	// Old is the exact text to replace; New is its replacement.
	Old string `json:"old"`
	New string `json:"new"`

	// Context explains where the change lands, e.g. "Pasal 12 ayat (3)".
	Context string `json:"context,omitempty"`

	// Rationale is a one-line justification shown next to the action.
	Rationale string `json:"rationale,omitempty"`
}

// Finding is one reported issue or observation.
type Finding struct {
	ID       string   `json:"id"`
	Class    Class    `json:"class"`
	Category Category `json:"category"`
	Severity Severity `json:"severity"`

	// Message is the human-readable explanation, already localized.
	Message string `json:"message"`

	// ParaIndex points at the paragraph the finding concerns, when applicable.
	// It is interpreted against the document named by Side.
	ParaIndex *int `json:"para_index,omitempty"`

	// Side says which document ParaIndex belongs to. Almost every finding is
	// about the new version, but a deletion can only be located in the old one —
	// rendering that index against the new document would point at unrelated
	// text. Empty means SideCurr.
	Side Side `json:"side,omitempty"`

	// NodeID is the structural node the finding concerns, when applicable.
	NodeID string `json:"node_id,omitempty"`

	// Reference carries the citation for reference-integrity findings.
	Reference string `json:"reference,omitempty"`

	// Evidence holds supporting quotes or observed/expected values.
	Evidence []string `json:"evidence,omitempty"`

	// Actions are the proposed fixes, possibly empty.
	Actions []Action `json:"actions,omitempty"`

	// Confidence is set for advisory findings only, in [0,1].
	Confidence float64 `json:"confidence,omitempty"`
}

// ChangeType classifies a text-level delta.
type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeRemoved  ChangeType = "removed"
	ChangeModified ChangeType = "modified"
)

// Change is one text difference between the two documents.
//
// PrevHTML and CurrHTML carry <b> markers around the differing words, produced
// directly by the word-level diff. mining-legal-backend spends a whole batched
// LLM stage ("Diff Highlight") computing this; here it falls out of the diff.
type Change struct {
	ID   int        `json:"id"`
	Type ChangeType `json:"type"`

	PrevIndexes []int `json:"prev_indexes,omitempty"`
	CurrIndexes []int `json:"curr_indexes,omitempty"`

	PrevText string `json:"prev_text,omitempty"`
	CurrText string `json:"curr_text,omitempty"`

	PrevHTML string `json:"prev_html,omitempty"`
	CurrHTML string `json:"curr_html,omitempty"`

	// Context is the structural path the change sits in, e.g. "Pasal 12 ayat (3)".
	Context string `json:"context,omitempty"`
	NodeID  string `json:"node_id,omitempty"`

	// TableRef locates the change inside a table when it is in one,
	// e.g. "tabel 2 baris 3 kolom 1". Empty otherwise.
	TableRef string `json:"table_ref,omitempty"`

	// Cosmetic marks changes that are whitespace/punctuation only. Cosmetic
	// changes are excluded from the LLM tier entirely.
	Cosmetic bool `json:"cosmetic,omitempty"`
}

// Summary is the headline count shown at the top of a report.
type Summary struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Modified int `json:"modified"`
	Total    int `json:"total"`

	Verified int `json:"verified_findings"`
	Advisory int `json:"advisory_findings"`

	Critical int `json:"critical"`
	Major    int `json:"major"`
	Minor    int `json:"minor"`
	Info     int `json:"info"`
}

// Report is the full comparison result.
type Report struct {
	PrevSource string `json:"prev_source"`
	CurrSource string `json:"curr_source"`

	Summary  Summary   `json:"summary"`
	Changes  []Change  `json:"changes"`
	Findings []Finding `json:"findings"`

	// PasalCount records the article-level count on each side, so a reviewer can
	// see at a glance that a document went from 24 to 25 articles.
	PrevArticleCount int `json:"prev_article_count"`
	CurrArticleCount int `json:"curr_article_count"`

	// Renumbering summarises the document-wide numbering plan, one line per
	// sequence ("Urutan Pasal: 2, 2, 4, 8, 6, 8 -> 1, 2, 3, 4, 5, 6"). It is
	// what lets a reviewer confirm that a set of individually small renumber
	// actions adds up to a correctly ordered document.
	Renumbering []string `json:"renumbering,omitempty"`

	// RenumberingWarnings lists defects that survive applying the plan. It
	// should always be empty; a non-empty value means the plan is incomplete
	// and must not be trusted as a full fix.
	RenumberingWarnings []string `json:"renumbering_warnings,omitempty"`
}

// AddFinding appends a finding and keeps the summary counters in step.
func (r *Report) AddFinding(f Finding) {
	if f.ID == "" {
		f.ID = fmt.Sprintf("%s-%d", f.Category, len(r.Findings)+1)
	}
	r.Findings = append(r.Findings, f)
	switch f.Class {
	case ClassVerified:
		r.Summary.Verified++
	case ClassAdvisory:
		r.Summary.Advisory++
	}
	switch f.Severity {
	case SeverityCritical:
		r.Summary.Critical++
	case SeverityMajor:
		r.Summary.Major++
	case SeverityMinor:
		r.Summary.Minor++
	case SeverityInfo:
		r.Summary.Info++
	}
}

// SortFindings orders findings by severity, then by paragraph position, so the
// most serious problems surface first and ties read in document order.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		ra, rb := severityRank(a.Severity), severityRank(b.Severity)
		if ra != rb {
			return ra < rb
		}
		pa, pb := -1, -1
		if a.ParaIndex != nil {
			pa = *a.ParaIndex
		}
		if b.ParaIndex != nil {
			pb = *b.ParaIndex
		}
		if pa != pb {
			return pa < pb
		}
		return a.ID < b.ID
	})
}

// IntPtr is a small helper for the many optional index fields.
func IntPtr(i int) *int { return &i }
