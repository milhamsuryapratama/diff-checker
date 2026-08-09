package jobs

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/trace"
)

func openTestDB(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := NewStore(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, path
}

// reopen simulates a restart: close nothing, open the same file again.
func reopen(t *testing.T, path string) *Store {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStore after restart: %v", err)
	}
	return s
}

// The whole point of the database: a finished comparison is still there after
// the process that produced it is gone.
func TestFinishedJobSurvivesRestart(t *testing.T) {
	s, path := openTestDB(t)

	s.Create("j1", "lama.docx", "baru.docx", "/p", "/c", true)
	rep := &docmodel.Report{PrevSource: "lama.docx", CurrSource: "baru.docx"}
	rep.AddFinding(docmodel.Finding{
		Class: docmodel.ClassVerified, Category: docmodel.CatNumberingGap,
		Severity: docmodel.SeverityMajor, Message: "Pasal 3 hilang",
	})
	s.update("j1", func(j *Job) {
		j.Status = StatusDone
		j.Report = rep
		j.CostUSD = 0.42
		j.FinishedAt = time.Now()
	})

	restored := reopen(t, path)

	j, err := restored.Get("j1")
	if err != nil {
		t.Fatalf("job lost across restart: %v", err)
	}
	if j.Status != StatusDone {
		t.Errorf("status = %q, want done", j.Status)
	}
	if j.Report == nil || len(j.Report.Findings) != 1 {
		t.Fatalf("report did not survive: %+v", j.Report)
	}
	if j.Report.Findings[0].Message != "Pasal 3 hilang" {
		t.Errorf("finding text lost: %+v", j.Report.Findings[0])
	}
	if j.CostUSD != 0.42 || !j.UseAI {
		t.Errorf("metadata lost: cost=%v useAI=%v", j.CostUSD, j.UseAI)
	}
}

// Reasoning is persisted too, or the record would only exist while the tab
// stayed open.
func TestTraceSurvivesRestart(t *testing.T) {
	s, path := openTestDB(t)
	s.Create("j2", "a", "b", "/p", "/c", false)

	rec := s.Recorder("j2")
	rec.Note("deterministic", trace.KindNote, "Membaca 120 paragraf.")
	rec.Stream("analyze", trace.KindThought, "Melihat perubahan ")
	rec.Stream("analyze", trace.KindThought, "jangka waktu.")
	rec.EndStream("analyze")

	restored := reopen(t, path)

	got := restored.Trace("j2")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after restart, got %d: %+v", len(got), got)
	}
	if got[0].Text != "Membaca 120 paragraf." {
		t.Errorf("note lost: %q", got[0].Text)
	}
	// A streamed line is upserted as it grows, so the stored row must hold the
	// complete text rather than the first chunk.
	if got[1].Text != "Melihat perubahan jangka waktu." {
		t.Errorf("streamed entry stored incomplete: %q", got[1].Text)
	}
	if got[1].Kind != trace.KindThought {
		t.Errorf("kind lost: %q", got[1].Kind)
	}
}

// A job that was running when the process died cannot be resumed: its pipeline
// state was never in the database. Leaving it "running" would show a progress
// page that never advances.
func TestInterruptedJobIsMarkedFailed(t *testing.T) {
	s, path := openTestDB(t)
	s.Create("j3", "a", "b", "/p", "/c", false)
	s.update("j3", func(j *Job) { j.Status = StatusRunning })

	restored := reopen(t, path)

	j, err := restored.Get("j3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if j.Status != StatusFailed {
		t.Errorf("status = %q, want failed", j.Status)
	}
	if j.Error == "" {
		t.Error("interrupted job carries no explanation")
	}
	for _, st := range j.Steps {
		if st.Status == "running" || st.Status == "pending" {
			t.Errorf("step %q left mid-flight after restart", st.Node)
		}
	}

	// And it must stay failed: the fix is persisted, not recomputed each boot.
	again := reopen(t, path)
	if j2, _ := again.Get("j3"); j2.Status != StatusFailed {
		t.Errorf("failure state not persisted: %q", j2.Status)
	}
}

func TestListIsNewestFirstAfterRestart(t *testing.T) {
	s, path := openTestDB(t)
	for _, id := range []string{"old", "mid", "new"} {
		s.Create(id, id+".docx", id+".docx", "/p", "/c", false)
		time.Sleep(2 * time.Millisecond)
	}

	got := reopen(t, path).List()

	if len(got) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(got))
	}
	if got[0].ID != "new" || got[2].ID != "old" {
		t.Errorf("order = %s, %s, %s; want new, mid, old", got[0].ID, got[1].ID, got[2].ID)
	}
}

// A nil database is a supported mode — tests and the CLI use it — and must not
// panic anywhere the store would otherwise write.
func TestNilDatabaseIsInMemory(t *testing.T) {
	s := mustStore(t)

	s.Create("j", "a", "b", "/p", "/c", false)
	s.Recorder("j").Note("n", trace.KindNote, "halo")
	s.update("j", func(j *Job) { j.Status = StatusDone })

	if j, err := s.Get("j"); err != nil || j.Status != StatusDone {
		t.Errorf("in-memory store broken: %v %+v", err, j)
	}
	if got := s.Trace("j"); len(got) != 1 {
		t.Errorf("in-memory trace = %d entries, want 1", len(got))
	}
}
