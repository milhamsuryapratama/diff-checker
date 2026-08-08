// Package httpx serves the web UI.
//
// Rendering is entirely server-side Go templates: no npm, no bundler, no build
// step. The one place that needs JavaScript is the progress page, because
// EventSource is a browser API with no HTML equivalent — that is roughly forty
// lines of vanilla JS whose only job is to swap innerHTML with fragments the
// server rendered.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/jobs"
)

// maxUpload bounds a single uploaded document.
//
// 32 MB comfortably covers a 200-page DOCX while keeping a malicious or
// mistaken upload from exhausting memory. It is enforced before parsing, since
// the parser is the expensive part.
const maxUpload = 32 << 20

// Server wires the routes.
type Server struct {
	store    *jobs.Store
	worker   *jobs.Worker
	registry *models.Registry
	uploads  string
	log      *slog.Logger
}

// New creates the server. uploadDir must exist and be writable.
func New(store *jobs.Store, worker *jobs.Worker, registry *models.Registry, uploadDir string, log *slog.Logger) *Server {
	return &Server{store: store, worker: worker, registry: registry, uploads: uploadDir, log: log}
}

// Routes returns the mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /compare", s.handleCompare)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /jobs/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /jobs/{id}/report", s.handleReport)
	mux.HandleFunc("GET /jobs/{id}/export.json", s.handleExport)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))

	return withLogging(s.log, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"ai_available": s.registry.Configured(),
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.gohtml", map[string]any{
		"AIAvailable": s.registry.Configured(),
		"AIMissing":   strings.Join(s.registry.Missing(), ", "),
		"Jobs":        s.store.List(),
	})
}

// handleCompare accepts the two uploads and starts a job.
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		s.fail(w, http.StatusBadRequest, "Unggahan terlalu besar atau rusak", err)
		return
	}

	id := newID()
	dir := filepath.Join(s.uploads, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.fail(w, http.StatusInternalServerError, "Tidak dapat menyiapkan direktori unggahan", err)
		return
	}

	prevName, prevPath, err := s.save(r, "prev", dir)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "Dokumen versi lama tidak dapat dibaca", err)
		return
	}
	currName, currPath, err := s.save(r, "curr", dir)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "Dokumen versi baru tidak dapat dibaca", err)
		return
	}

	// The AI tier is opt-in and only offered when it is actually configured,
	// so a job can never queue up against a provider it cannot reach.
	useAI := r.FormValue("use_ai") == "on" && s.registry.Configured()

	s.store.Create(id, prevName, currName, prevPath, currPath, useAI)
	if err := s.worker.Enqueue(id); err != nil {
		s.fail(w, http.StatusServiceUnavailable, "Server sedang sibuk", err)
		return
	}

	http.Redirect(w, r, "/jobs/"+id, http.StatusSeeOther)
}

// save writes one uploaded file to disk.
//
// The stored filename is derived from the form field, never from the client's
// filename: a browser is free to send "../../etc/passwd" as a filename, and
// the original is kept only as a display label.
func (s *Server) save(r *http.Request, field, dir string) (name, path string, err error) {
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	switch ext {
	case ".docx", ".pdf", ".txt", ".md":
	default:
		return "", "", fmt.Errorf("format %q tidak didukung (gunakan .docx, .pdf, .txt, atau .md)", ext)
	}

	path = filepath.Join(dir, field+ext)
	dst, err := os.Create(path)
	if err != nil {
		return "", "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, io.LimitReader(f, maxUpload)); err != nil {
		return "", "", err
	}
	return filepath.Base(hdr.Filename), path, nil
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		s.fail(w, http.StatusNotFound, "Job tidak ditemukan", err)
		return
	}
	// A finished job has nothing to stream; send the reader straight to the
	// result instead of showing a completed checklist they must click past.
	if job.Status == jobs.StatusDone {
		http.Redirect(w, r, "/jobs/"+job.ID+"/report", http.StatusSeeOther)
		return
	}
	s.render(w, "job.gohtml", map[string]any{"Job": job})
}

// handleEvents streams job updates as server-sent events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming tidak didukung", http.StatusInternalServerError)
		return
	}

	job, ch, cancel, err := s.store.Subscribe(r.PathValue("id"))
	if err != nil {
		http.Error(w, "job tidak ditemukan", http.StatusNotFound)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Buffering proxies would defeat the whole mechanism by holding events
	// until the response closes.
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(ev jobs.Event) bool {
		payload, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Send the current state first, so a browser that connects after the job
	// finished still gets the result rather than waiting for an event that
	// will never come.
	done := job.Status == jobs.StatusDone || job.Status == jobs.StatusFailed
	if !send(jobs.Event{Job: job, Done: done}) || done {
		return
	}

	// Keepalives stop intermediaries from closing an idle connection during a
	// long deterministic parse.
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !send(ev) || ev.Done {
				return
			}
		}
	}
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		s.fail(w, http.StatusNotFound, "Job tidak ditemukan", err)
		return
	}
	if job.Status != jobs.StatusDone {
		http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
		return
	}
	s.render(w, "report.gohtml", map[string]any{
		"Job":    job,
		"Report": job.Report,
	})
}

// handleExport serves the raw report. It doubles as the product's first API
// contract, which is why it is the same structure the pipeline produces rather
// than a view model shaped for the page.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "job tidak ditemukan", http.StatusNotFound)
		return
	}
	if job.Status != jobs.StatusDone {
		http.Error(w, "job belum selesai", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "diff-"+job.ID+".json"))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(job.Report)
}

func (s *Server) fail(w http.ResponseWriter, code int, msg string, err error) {
	s.log.Warn("permintaan gagal", "pesan", msg, "err", err)
	w.WriteHeader(code)
	s.render(w, "error.gohtml", map[string]any{"Message": msg, "Detail": err.Error()})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func withLogging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// SSE connections stay open for the life of a job; logging their
		// duration as request latency would be noise.
		if !strings.HasSuffix(r.URL.Path, "/events") {
			log.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
		}
	})
}
