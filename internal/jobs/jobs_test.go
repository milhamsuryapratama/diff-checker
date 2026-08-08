package jobs

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
)

func newTestWorker(t *testing.T) (*Store, *Worker) {
	t.Helper()
	pipeline, err := agentic.New(nil)
	if err != nil {
		t.Fatalf("agentic.New: %v", err)
	}
	store, err := NewStore(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	w := NewWorker(store, pipeline, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Start(context.Background())
	return store, w
}

func fixture(scenario, side string) string {
	return filepath.Join("..", "..", "testdata", "scenarios", scenario, side+".docx")
}

func waitFor(t *testing.T, s *Store, id string) *Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.Get(id)
		if err == nil && (j.Status == StatusDone || j.Status == StatusFailed) {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s tidak selesai", id)
	return nil
}

func TestWorkerRunsJobToCompletion(t *testing.T) {
	store, w := newTestWorker(t)

	store.Create("j1", "prev.docx", "curr.docx",
		fixture("kitchen_sink", "prev"), fixture("kitchen_sink", "curr"), false)
	if err := w.Enqueue("j1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job := waitFor(t, store, "j1")
	if job.Status != StatusDone {
		t.Fatalf("status = %q, error = %q", job.Status, job.Error)
	}
	if job.Report == nil || job.Report.Summary.Critical != 1 {
		t.Errorf("unexpected report: %+v", job.Report)
	}
	// Nothing may remain in a transient state once a job has finished, or the
	// progress checklist would appear stuck forever.
	for _, s := range job.Steps {
		if s.Status == "pending" || s.Status == "running" {
			t.Errorf("step %q left in %q after completion", s.Node, s.Status)
		}
	}
}

func TestWorkerMarksFailureAndUnblocksChecklist(t *testing.T) {
	store, w := newTestWorker(t)

	store.Create("bad", "prev.docx", "hilang.docx",
		fixture("kitchen_sink", "prev"), filepath.Join("..", "..", "testdata", "tidak-ada.docx"), false)
	if err := w.Enqueue("bad"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job := waitFor(t, store, "bad")
	if job.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", job.Status)
	}
	if job.Error == "" {
		t.Error("failed job carries no error message")
	}
	for _, s := range job.Steps {
		if s.Status == "pending" || s.Status == "running" {
			t.Errorf("step %q left in %q after failure", s.Node, s.Status)
		}
	}
}

// Subscribers must receive the terminal event, and a subscriber that stops
// reading must never be able to wedge the worker producing those events.
func TestSubscribeReceivesTerminalEvent(t *testing.T) {
	store, w := newTestWorker(t)

	store.Create("j2", "a", "b", fixture("text_only", "prev"), fixture("text_only", "curr"), false)

	_, _, ch, cancel, err := store.Subscribe("j2")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	// A second subscriber that never reads: publish must not block on it.
	_, _, _, cancelIdle, _ := store.Subscribe("j2")
	defer cancelIdle()

	if err := w.Enqueue("j2"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no terminal event received")
		case ev := <-ch:
			if ev.Done {
				if ev.Job.Status != StatusDone {
					t.Errorf("terminal status = %q, want done", ev.Job.Status)
				}
				return
			}
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	store := mustStore(t)
	store.Create("j3", "a", "b", "p", "c", false)

	_, _, ch, cancel, err := store.Subscribe("j3")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()

	// The channel is closed on cancel, so a receive returns immediately rather
	// than leaking a goroutine parked on it.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel still delivering after cancel")
		}
	case <-time.After(time.Second):
		t.Error("channel not closed by cancel")
	}
}

// The store is read by HTTP handlers while the worker mutates it. This exists
// to give the race detector something to find if that ever stops being safe.
func TestConcurrentReadsAndWrites(t *testing.T) {
	store, w := newTestWorker(t)

	const n = 6
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		store.Create(id, "prev.docx", "curr.docx",
			fixture("renumbering", "prev"), fixture("renumbering", "curr"), false)
		if err := w.Enqueue(id); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = store.List()
					for j := 0; j < n; j++ {
						_, _ = store.Get(string(rune('a' + j)))
					}
				}
			}
		}()
	}

	for i := 0; i < n; i++ {
		waitFor(t, store, string(rune('a'+i)))
	}
	close(stop)
	wg.Wait()
}

// A full queue must be reported, not silently swallowed or allowed to grow
// without bound.
func TestEnqueueRejectsWhenFull(t *testing.T) {
	w := &Worker{queue: make(chan string, 1)}

	if err := w.Enqueue("first"); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := w.Enqueue("second"); err == nil {
		t.Error("expected an error when the queue is full")
	}
}

func TestStepsMarkPaidNodesSkippedWithoutAI(t *testing.T) {
	steps := initialSteps(false)

	var seenPaid bool
	for _, s := range steps {
		if isPaidNode(s.Node) {
			seenPaid = true
			if s.Status != "skipped" {
				t.Errorf("paid node %q = %q, want skipped", s.Node, s.Status)
			}
		}
	}
	if !seenPaid {
		t.Fatal("checklist contains no paid nodes")
	}

	for _, s := range initialSteps(true) {
		if s.Status != "pending" {
			t.Errorf("with AI on, node %q = %q, want pending", s.Node, s.Status)
		}
	}
}

func mustStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}
