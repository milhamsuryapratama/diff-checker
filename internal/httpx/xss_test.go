package httpx

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report marks textdiff's <b> markup as trusted HTML. That is only sound if
// the document text inside it was escaped when the markup was built — so a
// document containing a <script> tag must render inert.
func TestDocumentContentCannotInjectScript(t *testing.T) {
	dir := t.TempDir()
	prev := filepath.Join(dir, "prev.txt")
	curr := filepath.Join(dir, "curr.txt")
	os.WriteFile(prev, []byte("Pasal 1\nJangka waktu 30 hari.\n"), 0o644)
	os.WriteFile(curr, []byte("Pasal 1\nJangka waktu <script>alert('xss')</script> hari.\n"), 0o644)

	h := newTestServer(t)
	id := uploadFiles(t, h, prev, curr)
	waitForJob(t, h, id)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/report", nil))
	body := rec.Body.String()

	if strings.Contains(body, "<script>alert") {
		t.Error("document content rendered as live markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("script tag not escaped in output")
	}
}

// uploadFiles posts two arbitrary files and returns the job ID.
func uploadFiles(t *testing.T, h http.Handler, prevPath, currPath string) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range []struct{ field, path string }{{"prev", prevPath}, {"curr", currPath}} {
		data, err := os.ReadFile(p.path)
		if err != nil {
			t.Fatalf("read %s: %v", p.path, err)
		}
		w, _ := mw.CreateFormFile(p.field, filepath.Base(p.path))
		_, _ = w.Write(data)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/compare", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("compare status = %d: %s", rec.Code, rec.Body.String())
	}
	return strings.TrimPrefix(rec.Header().Get("Location"), "/jobs/")
}
