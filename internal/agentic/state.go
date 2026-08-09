package agentic

import (
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/graph"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// graph.State is map[string]any, and the library validates field types with
// reflection at runtime rather than at compile time. Every node in this package
// therefore goes through the accessors below and never touches the map with a
// string literal: a typo in a key name becomes a compile error here instead of
// a nil that surfaces three nodes later.
const (
	keyPrevPath = "prev_path"
	keyCurrPath = "curr_path"
	keyPrevDoc  = "prev_doc"
	keyCurrDoc  = "curr_doc"
	keyReport   = "report"
	keyTriage   = "triage"
	keyAnalysis = "analysis"
	keyRecs     = "recommendations"
	keyUsage    = "usage"
	keyOptions  = "options"
	keySink     = "sink"
)

// Sink carries a run's outputs back to the caller.
//
// The executor surfaces state to the outside world through an event stream that
// JSON-encodes each delta. A *docmodel.IndexedDoc does not survive that trip —
// its node tree is a graph of parent pointers — and neither does the usage
// accumulator, which owns a mutex. Rather than flatten those types to fit a
// transport that the pipeline does not otherwise need, the run hands nodes a
// pointer to write their results into directly. The state map still carries
// everything for node-to-node flow; the sink is only how the finished report
// leaves the graph.
type Sink struct {
	mu     sync.Mutex
	report *docmodel.Report
	usage  *Usage
}

// NewSink creates the per-run output holder.
func NewSink() *Sink { return &Sink{usage: &Usage{}} }

func (s *Sink) setReport(r *docmodel.Report) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report = r
}

// Report returns the finished report, or nil if the run did not complete.
func (s *Sink) Report() *docmodel.Report {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

// Usage returns the run's accumulated cost.
func (s *Sink) Usage() *Usage {
	if s == nil {
		return &Usage{}
	}
	return s.usage
}

// Options carries per-job settings that the graph needs but that are not
// derived from the documents themselves.
type Options struct {
	// NoLLM runs the deterministic tier only. The graph still executes; it just
	// routes around every node that would cost money.
	NoLLM bool

	// MaxAdvisory caps how many changes are sent to the analyze node, bounding
	// worst-case spend on a document with hundreds of edits.
	MaxAdvisory int

	// AdjudicateBelow is the confidence threshold under which a critical or
	// major finding gets a second opinion. Zero disables adjudication.
	AdjudicateBelow float64
}

// DefaultOptions are the settings used when a caller does not say otherwise.
func DefaultOptions() Options {
	return Options{MaxAdvisory: 25, AdjudicateBelow: 0.75}
}

// NewSchema declares every field the graph carries, with the reducers the
// library needs to merge parallel branches.
//
// The two ingest branches run concurrently and write disjoint keys, so the
// default last-write-wins reducer is correct for them. Usage is the exception:
// it is written by several nodes and must accumulate, so it gets a merging
// reducer instead of being silently overwritten by whichever node finished last.
func NewSchema() *graph.StateSchema {
	s := graph.NewStateSchema()
	addAny(s, keyPrevPath)
	addAny(s, keyCurrPath)
	addAny(s, keyPrevDoc)
	addAny(s, keyCurrDoc)
	addAny(s, keyReport)
	addAny(s, keyTriage)
	addAny(s, keyAnalysis)
	addAny(s, keyRecs)
	addAny(s, keyOptions)
	addAny(s, keySink)
	s.AddField(keyUsage, graph.StateField{
		Type:            anyType,
		Reducer:         mergeUsage,
		Default:         func() any { return &Usage{} },
		DisableDeepCopy: true,
	})
	return s
}

// addAny registers a field that is passed by reference between nodes.
//
// DisableDeepCopy is required, not an optimisation. The executor deep-copies
// state between node transitions by default, which would hand each node its own
// clone of the parsed document, the accumulating usage counter, and the run's
// output sink — so a node's writes would land on a copy the next node never
// sees. It is safe here because these values flow forward through the graph and
// no two nodes mutate the same one concurrently: the parallel ingest branches
// write disjoint keys, and the usage counter carries its own lock.
func addAny(s *graph.StateSchema, key string) {
	s.AddField(key, graph.StateField{
		Type:            anyType,
		Reducer:         graph.DefaultReducer,
		DisableDeepCopy: true,
	})
}

// State wraps graph.State with typed reads. It is a view, not a copy: writes
// still go back through the map the library owns, returned as node output.
type State struct{ raw graph.State }

// Wrap adapts a raw graph state for typed access.
func Wrap(s graph.State) State { return State{raw: s} }

func (s State) PrevPath() string { return getOr[string](s.raw, keyPrevPath, "") }
func (s State) CurrPath() string { return getOr[string](s.raw, keyCurrPath, "") }

func (s State) PrevDoc() *docmodel.IndexedDoc {
	return getOr[*docmodel.IndexedDoc](s.raw, keyPrevDoc, nil)
}

func (s State) CurrDoc() *docmodel.IndexedDoc {
	return getOr[*docmodel.IndexedDoc](s.raw, keyCurrDoc, nil)
}

func (s State) Report() *docmodel.Report {
	return getOr[*docmodel.Report](s.raw, keyReport, nil)
}

func (s State) Triage() *TriageVerdict {
	return getOr[*TriageVerdict](s.raw, keyTriage, nil)
}

func (s State) Analysis() *AnalysisResult {
	return getOr[*AnalysisResult](s.raw, keyAnalysis, nil)
}

func (s State) Recommendations() *RecommendationResult {
	return getOr[*RecommendationResult](s.raw, keyRecs, nil)
}

func (s State) Usage() *Usage {
	if u := getOr[*Usage](s.raw, keyUsage, nil); u != nil {
		return u
	}
	return &Usage{}
}

func (s State) Options() Options {
	return getOr[Options](s.raw, keyOptions, DefaultOptions())
}

// Sink returns the run's output holder, never nil, so a node can publish
// without checking.
func (s State) Sink() *Sink {
	if k := getOr[*Sink](s.raw, keySink, nil); k != nil {
		return k
	}
	return NewSink()
}

// Docs returns both parsed documents, erroring if either ingest branch failed
// to populate its half. Nodes downstream of the join call this instead of
// nil-checking twice.
func (s State) Docs() (prev, curr *docmodel.IndexedDoc, err error) {
	prev, curr = s.PrevDoc(), s.CurrDoc()
	if prev == nil {
		return nil, nil, fmt.Errorf("dokumen versi lama belum terbaca")
	}
	if curr == nil {
		return nil, nil, fmt.Errorf("dokumen versi baru belum terbaca")
	}
	return prev, curr, nil
}

// Initial builds the state a run starts from, together with the sink the
// caller reads its results out of.
func Initial(prevPath, currPath string, opts Options) (graph.State, *Sink) {
	sink := NewSink()
	return graph.State{
		keyPrevPath: prevPath,
		keyCurrPath: currPath,
		keyOptions:  opts,
		keyUsage:    sink.usage,
		keySink:     sink,
	}, sink
}

// The setters below return the single-key maps that a NodeFunc hands back as
// its output; the executor merges them into the run state through the reducers.
func setPrevDoc(d *docmodel.IndexedDoc) graph.State { return graph.State{keyPrevDoc: d} }
func setCurrDoc(d *docmodel.IndexedDoc) graph.State { return graph.State{keyCurrDoc: d} }
func setReport(r *docmodel.Report) graph.State      { return graph.State{keyReport: r} }
func setTriage(t *TriageVerdict) graph.State        { return graph.State{keyTriage: t} }
func setAnalysis(a *AnalysisResult) graph.State     { return graph.State{keyAnalysis: a} }

func setRecommendations(r *RecommendationResult) graph.State {
	return graph.State{keyRecs: r}
}

func getOr[T any](s graph.State, key string, fallback T) T {
	if v, ok := s[key]; ok {
		if t, ok := v.(T); ok {
			return t
		}
	}
	return fallback
}
