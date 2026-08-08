package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/jobs"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()

	pipeline, err := agentic.New(nil)
	if err != nil {
		t.Fatalf("agentic.New: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	store := jobs.NewStore()
	worker := jobs.NewWorker(store, pipeline, 2, log)
	worker.Start(context.Background())

	// An empty registry means "AI not configured", which is the mode the tests
	// run in: no credentials, no network.
	registry := models.NewRegistry(map[models.Tier]models.Spec{})

	return New(store, worker, registry, t.TempDir(), log).Routes()
}

func fixturePath(scenario, side string) string {
	return filepath.Join("..", "..", "testdata", "scenarios", scenario, side+".docx")
}

// upload posts a document pair and returns the job ID from the redirect.
func upload(t *testing.T, h http.Handler, scenario string) string {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, side := range []string{"prev", "curr"} {
		data, err := os.ReadFile(fixturePath(scenario, side))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		w, err := mw.CreateFormFile(side, side+".docx")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/compare", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("compare status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	return strings.TrimPrefix(loc, "/jobs/")
}

// waitForJob polls the export endpoint until the job finishes.
func waitForJob(t *testing.T, h http.Handler, id string) *docmodel.Report {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/export.json", nil))
		if rec.Code == http.StatusOK {
			var rep docmodel.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			return &rep
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("job tidak selesai dalam batas waktu")
	return nil
}

// The full browser journey: upload, job runs, report is served, and the
// findings match what the CLI produces for the same fixture.
func TestUploadThroughToReport(t *testing.T) {
	h := newTestServer(t)
	id := upload(t, h, "kitchen_sink")

	rep := waitForJob(t, h, id)

	if rep.Summary.Critical != 1 || rep.Summary.Major != 3 {
		t.Errorf("summary = %+v, want 1 critical / 3 major", rep.Summary)
	}
	// Server paths must not leak into a document the user can download.
	if strings.Contains(rep.PrevSource, string(os.PathSeparator)) {
		t.Errorf("report leaks a server path: %q", rep.PrevSource)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("report page status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"terverifikasi", "broken_reference", "Pasal 6"} {
		if !strings.Contains(body, want) {
			t.Errorf("report page missing %q", want)
		}
	}
}

// With no AI configured the report must not claim any advisory analysis
// happened — the class split is only meaningful if it is honest.
func TestNoAdvisoryFindingsWithoutAI(t *testing.T) {
	h := newTestServer(t)
	rep := waitForJob(t, h, upload(t, h, "renumbering"))

	if rep.Summary.Advisory != 0 {
		t.Errorf("advisory findings = %d with AI disabled, want 0", rep.Summary.Advisory)
	}
	for _, f := range rep.Findings {
		if f.Class != docmodel.ClassVerified {
			t.Errorf("non-verified finding with AI disabled: %+v", f)
		}
	}
}

// The SSE stream is how the progress page learns anything; a job that finished
// before the browser connected must still receive its terminal event.
func TestEventStreamDeliversTerminalSnapshot(t *testing.T) {
	h := newTestServer(t)
	id := upload(t, h, "text_only")
	waitForJob(t, h, id)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/events", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "data: ") {
		t.Fatalf("stream does not start with an SSE data frame: %q", truncate(body, 80))
	}

	var ev jobs.Event
	payload := strings.TrimSuffix(strings.TrimPrefix(body, "data: "), "\n\n")
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if !ev.Done {
		t.Error("terminal event not marked done; the browser would wait forever")
	}
	if ev.Job.Status != jobs.StatusDone {
		t.Errorf("job status = %q, want done", ev.Job.Status)
	}
	// Nodes the conditional edge routed past must read as skipped, not stuck.
	for _, s := range ev.Job.Steps {
		if s.Node == agentic.NodeAnalyze && s.Status != "skipped" {
			t.Errorf("analyze step = %q, want skipped", s.Status)
		}
	}
}

func TestRejectsUnsupportedFormat(t *testing.T) {
	h := newTestServer(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, side := range []string{"prev", "curr"} {
		w, _ := mw.CreateFormFile(side, side+".exe")
		_, _ = w.Write([]byte("MZ"))
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/compare", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unsupported format", rec.Code)
	}
}

// A client-supplied filename must never influence where a file lands.
func TestUploadIgnoresClientPath(t *testing.T) {
	h := newTestServer(t)
	dir := t.TempDir()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, side := range []string{"prev", "curr"} {
		data, _ := os.ReadFile(fixturePath("text_only", side))
		w, _ := mw.CreateFormFile(side, "../../../../etc/passwd.docx")
		_, _ = w.Write(data)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/compare", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	// Nothing may have escaped the upload root.
	if _, err := os.Stat(filepath.Join(dir, "..", "etc")); err == nil {
		t.Error("upload escaped the upload directory")
	}
}

func TestUnknownJobIs404(t *testing.T) {
	h := newTestServer(t)
	for _, path := range []string{"/jobs/deadbeef", "/jobs/deadbeef/export.json", "/jobs/deadbeef/events"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestIndexAndHealth(t *testing.T) {
	h := newTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}
	// With no provider configured the AI checkbox must be disabled rather than
	// offered and then failing at run time.
	if !strings.Contains(rec.Body.String(), "Analisis AI tidak tersedia") {
		t.Error("index does not disable the AI option when unconfigured")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var health map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("health decode: %v", err)
	}
	if health["ai_available"] != false {
		t.Errorf("ai_available = %v, want false", health["ai_available"])
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
