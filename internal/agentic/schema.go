// Package agentic holds the LLM tier of the pipeline: the graph that
// orchestrates it, the tools it can call, and the typed contracts it must
// answer in.
//
// Everything here produces docmodel.ClassAdvisory findings. The deterministic
// tier (ingest → structure → textdiff → rules) never runs through this package,
// which is what makes its output reproducible. Two rules keep that separation
// honest:
//
//   - No node in this package may create, edit, or suppress a verified finding.
//   - Every advisory action must survive internal/ground before a user sees it.
package agentic

// ChangeKind is the closed taxonomy of legal change types.
//
// A closed enum, rather than free text, is the first of the four domain assets
// the product depends on: it turns "the model described the change" into "the
// model classified the change", which is the only form that can be scored
// against a golden set or trended over time. The values are carried into the
// JSON schema as an enum, so a response outside this list is rejected by the
// structured-output layer rather than reaching a reviewer.
type ChangeKind string

const (
	KindTypo               ChangeKind = "typo"
	KindFormatting         ChangeKind = "formatting"
	KindNumberingShift     ChangeKind = "numbering_shift"
	KindReferenceUpdate    ChangeKind = "reference_update"
	KindDefinitionChange   ChangeKind = "definition_change"
	KindObligationAdded    ChangeKind = "obligation_added"
	KindObligationRemoved  ChangeKind = "obligation_removed"
	KindLiabilityShift     ChangeKind = "liability_shift"
	KindTermExtension      ChangeKind = "term_extension"
	KindPenaltyChange      ChangeKind = "penalty_change"
	KindGoverningLawChange ChangeKind = "governing_law_change"
	KindPartyChange        ChangeKind = "party_change"
)

// AllChangeKinds is the enum as rendered into prompts and schemas. Order is
// stable so the prompt prefix stays byte-identical between runs, which is a
// precondition for prompt caching.
var AllChangeKinds = []ChangeKind{
	KindTypo, KindFormatting, KindNumberingShift, KindReferenceUpdate,
	KindDefinitionChange, KindObligationAdded, KindObligationRemoved,
	KindLiabilityShift, KindTermExtension, KindPenaltyChange,
	KindGoverningLawChange, KindPartyChange,
}

// Valid reports whether k is a member of the taxonomy.
func (k ChangeKind) Valid() bool {
	for _, c := range AllChangeKinds {
		if c == k {
			return true
		}
	}
	return false
}

// Cosmetic reports whether a classification means "no legal effect". These are
// filtered out before the recommend tier so the expensive model only sees
// changes that could actually matter.
func (k ChangeKind) Cosmetic() bool {
	return k == KindTypo || k == KindFormatting
}

// TriageVerdict is the cheap first-pass answer: is there anything here worth
// paying a larger model to look at?
type TriageVerdict struct {
	// Substantive is false when every change is formatting, whitespace, or
	// typographical. False short-circuits the rest of the LLM tier entirely.
	Substantive bool `json:"substantive" jsonschema:"description=true if at least one change could alter legal meaning"`

	// ChangeIDs lists the Change.ID values worth analysing further. Ignored
	// when Substantive is false.
	ChangeIDs []int `json:"change_ids" jsonschema:"description=IDs of changes that need deeper analysis"`

	// Reason is one sentence, shown in the UI when the pipeline stops early.
	Reason string `json:"reason" jsonschema:"description=one sentence justification in Indonesian"`
}

// AnalyzedChange is the analyze node's answer for a single text change.
type AnalyzedChange struct {
	// ChangeID matches docmodel.Change.ID. The grounding validator rejects any
	// ID that does not correspond to a real change.
	ChangeID int `json:"change_id"`

	Kind ChangeKind `json:"kind" jsonschema:"description=one value from the closed taxonomy"`

	// Severity must be one of critical/major/minor/info, judged against the
	// rubric in the prompt rather than the model's own instinct.
	Severity string `json:"severity" jsonschema:"description=critical, major, minor, or info"`

	// Summary is one sentence in the document's language.
	Summary string `json:"summary"`

	// Confidence in [0,1]. Values below the adjudication threshold route the
	// finding to a second opinion instead of straight to the report.
	Confidence float64 `json:"confidence"`
}

// AnalysisResult is the analyze node's full structured response.
type AnalysisResult struct {
	Changes []AnalyzedChange `json:"changes"`
}

// Recommendation is one legal-risk finding with an optional concrete fix.
type Recommendation struct {
	// ChangeID ties the recommendation back to a text change, or -1 when it
	// stands on its own (e.g. a risk that emerges from a deletion).
	ChangeID int `json:"change_id"`

	Severity string `json:"severity" jsonschema:"description=critical, major, minor, or info"`

	// Risk states the legal exposure in one or two sentences.
	Risk string `json:"risk"`

	// Recommendation is the advised course of action, in prose.
	Recommendation string `json:"recommendation"`

	// ParagraphIndex is the paragraph in the NEW document this concerns.
	// Required whenever Old is set, because grounding checks Old against it.
	ParagraphIndex *int `json:"paragraph_index,omitempty"`

	// Old and New describe an optional literal edit. Old must appear verbatim
	// in the referenced paragraph or the whole action is dropped.
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`

	Confidence float64 `json:"confidence"`
}

// RecommendationResult is the recommend node's full structured response.
type RecommendationResult struct {
	Recommendations []Recommendation `json:"recommendations"`
}

// AdjudicationResult is the second-opinion node's answer. It never introduces
// new findings; it only agrees or disagrees with one already on the table.
type AdjudicationResult struct {
	// Agree is false when the reviewing model reaches a different conclusion,
	// which downgrades confidence and marks the finding for human review.
	Agree bool `json:"agree"`

	// Severity is the reviewer's own rating, used when it is lower than the
	// original — a disagreement should never escalate on its own.
	Severity string `json:"severity" jsonschema:"description=critical, major, minor, or info"`

	Reason string `json:"reason"`
}
