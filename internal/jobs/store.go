// Package jobs runs comparisons in the background and reports their progress.
//
// A comparison takes seconds without the AI tier and up to a minute with it,
// which is too long to hold an HTTP request open and far too long to leave a
// browser showing nothing. Uploading therefore creates a job and redirects; the
// job page subscribes to the job's event stream and fills in a checklist as
// each pipeline node completes.
package jobs

import (
	"errors"
	"sync"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// ErrNotFound is returned for an unknown job ID.
var ErrNotFound = errors.New("job tidak ditemukan")

// Status is where a job is in its life.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Step is one pipeline node's state, as shown on the progress checklist.
type Step struct {
	Node   string `json:"node"`
	Label  string `json:"label"`
	Status string `json:"status"` // pending | running | done | skipped | error
}

// Job is one comparison.
type Job struct {
	ID     string `json:"id"`
	Status Status `json:"status"`

	PrevName string `json:"prev_name"`
	CurrName string `json:"curr_name"`

	// prevPath and currPath are the uploaded copies on disk. They are not
	// exported: a job's JSON is served to the browser, and the server's
	// filesystem layout is not the browser's business.
	prevPath string
	currPath string

	UseAI bool `json:"use_ai"`

	Steps []Step `json:"steps"`

	Report  *docmodel.Report    `json:"report,omitempty"`
	Usage   []agentic.NodeUsage `json:"usage,omitempty"`
	CostUSD float64             `json:"cost_usd"`

	Error string `json:"error,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Duration is how long the job took, or has been running.
func (j *Job) Duration() time.Duration {
	if j.FinishedAt.IsZero() {
		return time.Since(j.CreatedAt)
	}
	return j.FinishedAt.Sub(j.CreatedAt)
}

// Store holds jobs in memory.
//
// In-memory is a deliberate v1 choice, not an oversight: a job's inputs are
// uploaded files that live in a temp directory for the life of the process, so
// persisting job metadata past a restart would leave rows pointing at files
// that no longer exist. Durable jobs arrive together with durable artifact
// storage, not before it.
type Store struct {
	mu   sync.RWMutex
	jobs map[string]*Job
	subs map[string][]chan Event
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{jobs: map[string]*Job{}, subs: map[string][]chan Event{}}
}

// Create registers a new job in the queued state with its checklist laid out.
func (s *Store) Create(id, prevName, currName, prevPath, currPath string, useAI bool) *Job {
	j := &Job{
		ID:        id,
		Status:    StatusQueued,
		PrevName:  prevName,
		CurrName:  currName,
		prevPath:  prevPath,
		currPath:  currPath,
		UseAI:     useAI,
		Steps:     initialSteps(useAI),
		CreatedAt: time.Now(),
	}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	return j
}

// initialSteps builds the checklist.
//
// With the AI tier off, the paid nodes are shown as skipped rather than hidden.
// That is a product decision: a user on the free tier should be able to see
// exactly which analysis they are not getting.
func initialSteps(useAI bool) []Step {
	var steps []Step
	for _, node := range agentic.OrderedNodes {
		st := "pending"
		if !useAI && isPaidNode(node) {
			st = "skipped"
		}
		steps = append(steps, Step{Node: node, Label: agentic.NodeLabels[node], Status: st})
	}
	return steps
}

func isPaidNode(node string) bool {
	switch node {
	case agentic.NodeTriage, agentic.NodeAnalyze, agentic.NodeRecommend:
		return true
	}
	return false
}

// Get returns a snapshot of a job.
//
// It is a copy: callers render it into a template or serialise it while the
// worker may still be mutating the original.
func (s *Store) Get(id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *j
	clone.Steps = append([]Step(nil), j.Steps...)
	return &clone, nil
}

// List returns every job, newest first, for the index page.
func (s *Store) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		clone := *j
		out = append(out, &clone)
	}
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

// paths returns a job's input files, for the worker.
func (s *Store) paths(id string) (prev, curr string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, exists := s.jobs[id]
	if !exists {
		return "", "", false
	}
	return j.prevPath, j.currPath, true
}

// update applies a mutation to a job under lock and publishes the result.
func (s *Store) update(id string, fn func(*Job)) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if ok {
		fn(j)
	}
	s.mu.Unlock()
	if ok {
		s.publish(id)
	}
}
