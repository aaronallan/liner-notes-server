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

// featuresResponse mirrors the real ReccoBeats /v1/audio-features payload: a
// "content" array of feature objects, each carrying the ReccoBeats id, the
// Spotify href, and the audio-feature fields.
const featuresResponse = `{
  "content": [
    {
      "id": "505e3d6f-82fb-4478-b68a-94ad1a0c9ad8",
      "href": "https://open.spotify.com/track/track-abc",
      "isrc": "USQX92504223",
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
    }
  ]
}`

const emptyResponse = `{"content":[]}`

func TestClient_AudioFeatures(t *testing.T) {
	var gotPath, gotIDs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIDs = r.URL.Query().Get("ids")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	feats, err := c.AudioFeatures(context.Background(), "track-abc")
	if err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}

	// The single-track path requires a ReccoBeats UUID; the batch
	// /v1/audio-features endpoint is the one that accepts Spotify IDs.
	if gotPath != "/v1/audio-features" {
		t.Errorf("path = %q, want /v1/audio-features", gotPath)
	}
	if gotIDs != "track-abc" {
		t.Errorf("ids = %q, want track-abc", gotIDs)
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

func TestClient_EmptyContentNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// ReccoBeats returns 200 with an empty content array for unknown IDs.
		_, _ = w.Write([]byte(emptyResponse))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.AudioFeatures(context.Background(), "not-in-db")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if httpx.IsRetryable(err) {
		t.Error("a missing track should not be retryable")
	}
}

func TestClient_SendsSpotifyIDAsQuery(t *testing.T) {
	var gotIDs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIDs = r.URL.Query().Get("ids")
		_, _ = w.Write([]byte(featuresResponse))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.AudioFeatures(context.Background(), "weird/id space"); err != nil {
		t.Fatalf("AudioFeatures error: %v", err)
	}
	// The id must arrive intact after query-escaping/decoding.
	if gotIDs != "weird/id space" {
		t.Errorf("ids = %q, want %q (query-escaped in transit)", gotIDs, "weird/id space")
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
	if rec["found"] != true {
		t.Errorf("found = %v, want true", rec["found"])
	}
	if _, ok := rec["dur"]; !ok {
		t.Error("dur missing from log record")
	}
}

func TestClient_LogsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(emptyResponse))
	}))
	t.Cleanup(srv.Close)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	c := NewClient(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	_, _ = c.AudioFeatures(context.Background(), "track-abc")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	// A 200 with no features must be visibly distinct from a successful hit,
	// otherwise the log can't explain why a scan got no audio features.
	if rec["found"] != false {
		t.Errorf("found = %v, want false", rec["found"])
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
