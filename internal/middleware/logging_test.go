package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newCapture returns a logger that writes JSON records into buf, and the buf.
func newCapture() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, nil)), buf
}

// decodeLine parses the single JSON log record written to buf.
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	return rec
}

func TestLogging_LogsRequestLine(t *testing.T) {
	logger, buf := newCapture()
	h := Logging(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/lookup", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	rec := decodeLine(t, buf)
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if rec["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", rec["method"])
	}
	if rec["path"] != "/v1/lookup" {
		t.Errorf("path = %v, want /v1/lookup", rec["path"])
	}
	if rec["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", rec["status"])
	}
	if _, ok := rec["dur"]; !ok {
		t.Error("dur missing from log record")
	}
}

func TestLogging_CapturesStatusCode(t *testing.T) {
	logger, buf := newCapture()
	h := Logging(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	if got := decodeLine(t, buf)["status"]; got != float64(http.StatusBadRequest) {
		t.Errorf("status = %v, want 400", got)
	}
}

func TestLogging_DefaultsToOK(t *testing.T) {
	logger, buf := newCapture()
	// Handler writes a body without ever calling WriteHeader explicitly.
	h := Logging(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	if got := decodeLine(t, buf)["status"]; got != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200 (default)", got)
	}
}

func TestLogging_PassesResponseThrough(t *testing.T) {
	logger, _ := newCapture()
	h := Logging(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (passed through)", rec.Code)
	}
	if body := rec.Body.String(); body != `{"ok":true}` {
		t.Errorf("body = %q, want passed through unchanged", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want passed through unchanged", ct)
	}
}
