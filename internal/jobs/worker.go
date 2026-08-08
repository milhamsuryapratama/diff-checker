package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
)

// jobTimeout is the ceiling on a single comparison.
//
// The deterministic tier finishes in seconds; the AI tier is bounded by its own
// per-call timeouts and retry budget. This is the backstop that guarantees a
// job cannot occupy a worker forever — the failure mode that mining-legal-backend
// needs a CleanupStuckDocuments sweeper to recover from.
const jobTimeout = 10 * time.Minute

// Worker runs queued jobs on a bounded pool.
type Worker struct {
	store    *Store
	pipeline *agentic.Pipeline
	log      *slog.Logger

	// queue's capacity is the admission limit; a full queue rejects new work
	// rather than growing without bound.
	queue chan string
	// sem bounds how many comparisons run at once. Without it, a burst of
	// uploads would fan out unbounded LLM calls — the exact mistake the plan
	// calls out in the system being replaced.
	sem chan struct{}
}

// NewWorker creates a worker pool. concurrency is how many jobs may run at once.
func NewWorker(store *Store, pipeline *agentic.Pipeline, concurrency int, log *slog.Logger) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Worker{
		store:    store,
		pipeline: pipeline,
		log:      log,
		queue:    make(chan string, 64),
		sem:      make(chan struct{}, concurrency),
	}
}

// Start begins consuming the queue and returns immediately.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-w.queue:
				select {
				case <-ctx.Done():
					return
				case w.sem <- struct{}{}:
				}
				go func(id string) {
					defer func() { <-w.sem }()
					w.run(ctx, id)
				}(id)
			}
		}
	}()
}

// Enqueue submits a job. It returns an error when the queue is full rather than
// blocking the HTTP handler that called it.
func (w *Worker) Enqueue(id string) error {
	select {
	case w.queue <- id:
		return nil
	default:
		return fmt.Errorf("antrean penuh, coba lagi sebentar lagi")
	}
}

// run executes one job, publishing progress as it goes.
func (w *Worker) run(ctx context.Context, id string) {
	prevPath, currPath, ok := w.store.paths(id)
	if !ok {
		return
	}

	var useAI bool
	w.store.update(id, func(j *Job) {
		j.Status = StatusRunning
		useAI = j.UseAI
	})

	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	opts := agentic.DefaultOptions()
	opts.NoLLM = !useAI

	onProgress := func(p agentic.Progress) {
		w.store.update(id, func(j *Job) { applyProgress(j, p) })
	}

	res, err := w.pipeline.Run(ctx, prevPath, currPath, opts, onProgress)
	if err != nil {
		w.log.Error("job gagal", "job", id, "err", err)
		w.store.update(id, func(j *Job) {
			j.Status = StatusFailed
			j.Error = err.Error()
			j.FinishedAt = time.Now()
			// Anything still pending will never run; say so rather than
			// leaving the checklist frozen mid-stride.
			for i := range j.Steps {
				if j.Steps[i].Status == "pending" || j.Steps[i].Status == "running" {
					j.Steps[i].Status = "error"
				}
			}
		})
		return
	}

	usage := res.Usage.Nodes()
	total := res.Usage.Totals()

	w.store.update(id, func(j *Job) {
		j.Status = StatusDone
		// The pipeline records the on-disk path it parsed. That is the upload
		// directory, which is the server's business and not the reader's, so
		// the report is relabelled with the names the user actually uploaded
		// before it is rendered or exported.
		res.Report.PrevSource = j.PrevName
		res.Report.CurrSource = j.CurrName
		j.Report = res.Report
		j.Usage = usage
		j.CostUSD = total.USD
		j.FinishedAt = time.Now()
		// A node the conditional edge routed around never emitted an event, so
		// it is still pending here. It was skipped, not lost.
		for i := range j.Steps {
			if j.Steps[i].Status == "pending" || j.Steps[i].Status == "running" {
				j.Steps[i].Status = "skipped"
			}
		}
	})
	w.log.Info("job selesai", "job", id,
		"temuan", len(res.Report.Findings), "biaya_usd", total.USD, "panggilan", total.Calls)
}

// applyProgress folds one pipeline event into the job's checklist.
func applyProgress(j *Job, p agentic.Progress) {
	for i := range j.Steps {
		if j.Steps[i].Node != p.Node {
			continue
		}
		switch p.Status {
		case "start":
			j.Steps[i].Status = "running"
		case "done":
			j.Steps[i].Status = "done"
		case "error":
			j.Steps[i].Status = "error"
		}
		return
	}
}
