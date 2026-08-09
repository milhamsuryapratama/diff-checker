package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/rules"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

// debugDoc is one side's parse result: everything the pipeline knows about a
// document before any diffing or LLM tier touches it.
type debugDoc struct {
	Source       string               `json:"source"`
	Language     string               `json:"language"`
	ArticleCount int                  `json:"article_count"`
	Paragraphs   []docmodel.Paragraph `json:"paragraphs"`
	References   []docmodel.Reference `json:"references"`
	// Tree is the full BAB/Pasal/ayat hierarchy. Node already carries json
	// tags for its own fields and excludes Parent to avoid a cycle, so the
	// struct serialises as-is.
	Tree *docmodel.Node `json:"tree"`
}

// sequencePlan mirrors rules.SequencePlan with snake_case tags, so the debug
// payload reads consistently with the rest of the API instead of exposing the
// Go-exported field names of an internal type.
type sequencePlan struct {
	Label   string   `json:"label"`
	Scope   string   `json:"scope,omitempty"`
	Before  []string `json:"before"`
	After   []string `json:"after"`
	Fixes   int      `json:"fixes"`
	Changed bool     `json:"changed"`
}

// debugRenumbering exposes both sides of the mechanism behind report.gohtml's
// "USUL: RENUMBER" badges: the document-wide plan that actually ships, and the
// raw findings each local sequence check produced before assemble.go's
// attachPlanned overwrote their action with the plan's (or, when the plan has
// no entry for that paragraph, with nothing at all). Comparing the two is how
// a "verified, no action" finding gets diagnosed instead of guessed at.
type debugRenumbering struct {
	Sequences   []sequencePlan          `json:"sequences"`
	ByParagraph map[int]docmodel.Action `json:"by_paragraph"`
	RawFindings []docmodel.Finding      `json:"raw_numbering_findings"`
}

type debugPayload struct {
	JobID       string             `json:"job_id"`
	Prev        debugDoc           `json:"prev"`
	Curr        debugDoc           `json:"curr"`
	Renumbering debugRenumbering   `json:"renumbering"`
	Parallel    []docmodel.Finding `json:"parallel_findings"`
}

// handleDebug re-parses a job's source documents and returns the structure
// the deterministic tier computed from them: paragraph list, heading tree,
// references, and the renumbering plan with the raw per-sequence findings it
// superseded.
//
// This is deliberately computed on request rather than persisted alongside
// the report. The parse tree is a graph of parent pointers rebuilt fresh on
// every pipeline run (see the Sink comment in internal/agentic/state.go); the
// alternative — serialising a snapshot into every job row — would double
// storage for every comparison to serve a path only used for debugging.
// Re-parsing costs milliseconds and the source files already live in
// UPLOAD_DIR for the life of the job, so nothing is lost by not caching it.
func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	prevPath, currPath, ok := s.store.DocumentPaths(id)
	if !ok {
		http.Error(w, "job tidak ditemukan", http.StatusNotFound)
		return
	}

	prev, err := parseForDebug(prevPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("gagal membaca dokumen lama: %s", err), http.StatusInternalServerError)
		return
	}
	curr, err := parseForDebug(currPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("gagal membaca dokumen baru: %s", err), http.StatusInternalServerError)
		return
	}

	plan := rules.PlanRenumbering(curr)
	var sequences []sequencePlan
	for _, sp := range plan.Sequences {
		sequences = append(sequences, sequencePlan{
			Label: sp.Label, Scope: sp.Scope, Before: sp.Before, After: sp.After,
			Fixes: sp.Fixes, Changed: sp.Changed(),
		})
	}

	payload := debugPayload{
		JobID: id,
		Prev:  toDebugDoc(prev),
		Curr:  toDebugDoc(curr),
		Renumbering: debugRenumbering{
			Sequences:   sequences,
			ByParagraph: plan.ByParagraph,
			RawFindings: rules.ValidateNumbering(curr),
		},
		Parallel: rules.CheckParallelSequences(curr),
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func parseForDebug(path string) (*docmodel.IndexedDoc, error) {
	doc, err := ingest.Parse(path)
	if err != nil {
		return nil, err
	}
	structure.Build(doc)
	return doc, nil
}

func toDebugDoc(doc *docmodel.IndexedDoc) debugDoc {
	return debugDoc{
		Source:       doc.Source,
		Language:     doc.Lang,
		ArticleCount: structure.ArticleCount(doc),
		Paragraphs:   doc.Paragraphs,
		References:   doc.References,
		Tree:         doc.Root,
	}
}
