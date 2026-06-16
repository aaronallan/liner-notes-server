package reccobeats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

const featuresResponse = `{
  "acousticness": 0.012,
  "danceability": 0.735,
  "energy": 0.578,
  "instrumentalness": 0.0001,
  "liveness": 0.104,
  "loudness": -8.3,
  "speechiness": 0.041,
  "tempo": 119.8,
  "valence": 0.624,
  "key": 5,
  "mode": 1
}`

func TestClient_AudioFeatures(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	feats, err := c.AudioFeatures(context.Background(), "track-abc")
	if err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}

	if gotPath != "/v1/track/track-abc/audio-features" {
		t.Errorf("path = %q, want /v1/track/track-abc/audio-features", gotPath)
	}
	if feats.Danceability != 0.735 {
		t.Errorf("Danceability = %v, want 0.735", feats.Danceability)
	}
	if feats.Tempo != 119.8 {
		t.Errorf("Tempo = %v, want 119.8", feats.Tempo)
	}
	if feats.Key != 5 {
		t.Errorf("Key = %d, want 5", feats.Key)
	}
	if feats.Mode != 1 {
		t.Errorf("Mode = %d, want 1", feats.Mode)
	}
}

func TestClient_EscapesSpotifyID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.AudioFeatures(context.Background(), "weird/id space"); err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}
	if gotPath != "/v1/track/weird%2Fid%20space/audio-features" {
		t.Errorf("path = %q, want id path-escaped", gotPath)
	}
}

func TestClient_LogsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	if _, err := c.AudioFeatures(context.Background(), "track-abc"); err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	if rec["spotify_id"] != "track-abc" {
		t.Errorf("spotify_id = %v, want track-abc", rec["spotify_id"])
	}
	if rec["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", rec["status"])
	}
	if _, ok := rec["dur"]; !ok {
		t.Error("dur missing from log record")
	}
}

func TestClient_LogsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // closed server → transport error, no response/status

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	if _, err := c.AudioFeatures(context.Background(), "track-abc"); err == nil {
		t.Fatal("expected a transport error")
	}

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["spotify_id"] != "track-abc" {
		t.Errorf("spotify_id = %v, want track-abc", rec["spotify_id"])
	}
	if _, ok := rec["err"]; !ok {
		t.Error("err missing from log record")
	}
}

func TestClient_NoLoggerByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	// No WithLogger: must not panic and must default to discarding logs.
	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.AudioFeatures(context.Background(), "track-abc"); err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}
}

func TestClient_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.AudioFeatures(context.Background(), "x")
	var rl *httpx.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *httpx.RateLimitError, got %T (%v)", err, err)
	}
}

func TestClient_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.AudioFeatures(context.Background(), "missing")
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *httpx.APIError, got %T (%v)", err, err)
	}
	if httpx.IsRetryable(err) {
		t.Error("404 should not be retryable")
	}
}
