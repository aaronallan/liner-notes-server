package spotify

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

// staticToken is a TokenProvider that returns a fixed token, isolating the
// search client tests from the auth flow.
type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

type errToken struct{}

func (errToken) Token(context.Context) (string, error) { return "", errors.New("no token") }

const searchResponseJSON = `{
  "tracks": {
    "items": [
      {
        "id": "track-abc",
        "name": "Such Great Heights",
        "artists": [{"name": "The Postal Service"}],
        "external_ids": {"isrc": "USSUB0500001"}
      }
    ]
  }
}`

func TestClient_SearchByISRC(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("q")
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("type") != "track" {
			t.Errorf("type = %q, want track", r.URL.Query().Get("type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("tok-xyz"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "USSUB0500001")
	if err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}

	if gotPath != "/search" {
		t.Errorf("path = %q, want /search", gotPath)
	}
	if gotQuery != "isrc:USSUB0500001" {
		t.Errorf("q = %q, want isrc:USSUB0500001", gotQuery)
	}
	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("Authorization = %q, want Bearer tok-xyz", gotAuth)
	}

	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	tr := tracks[0]
	if tr.ID != "track-abc" {
		t.Errorf("ID = %q, want track-abc", tr.ID)
	}
	if tr.Name != "Such Great Heights" {
		t.Errorf("Name = %q", tr.Name)
	}
	if tr.ISRC != "USSUB0500001" {
		t.Errorf("ISRC = %q", tr.ISRC)
	}
	if len(tr.Artists) != 1 || tr.Artists[0] != "The Postal Service" {
		t.Errorf("Artists = %v", tr.Artists)
	}
}

func TestClient_SearchByTitleArtist(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.SearchByTitleArtist(context.Background(), "Such Great Heights", "The Postal Service"); err != nil {
		t.Fatalf("SearchByTitleArtist error: %v", err)
	}
	want := `track:Such Great Heights artist:The Postal Service`
	if gotQuery != want {
		t.Errorf("q = %q, want %q", gotQuery, want)
	}
}

func TestClient_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tracks":{"items":[]}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "NOPE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestClient_LogsSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	if _, err := c.SearchByISRC(context.Background(), "USSUB0500001"); err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}

	rec := decodeLog(t, buf)
	if rec["query"] != "isrc:USSUB0500001" {
		t.Errorf("query = %v, want isrc:USSUB0500001", rec["query"])
	}
	if rec["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", rec["status"])
	}
	if rec["results"] != float64(1) {
		t.Errorf("results = %v, want 1", rec["results"])
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
	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	if _, err := c.SearchByISRC(context.Background(), "x"); err == nil {
		t.Fatal("expected a transport error")
	}

	rec := decodeLog(t, buf)
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["query"] != "isrc:x" {
		t.Errorf("query = %v, want isrc:x", rec["query"])
	}
	if _, ok := rec["err"]; !ok {
		t.Error("err missing from log record")
	}
}

func TestClient_LogsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithLogger(logger))
	if _, err := c.SearchByISRC(context.Background(), "x"); err == nil {
		t.Fatal("expected an upstream error")
	}

	rec := decodeLog(t, buf)
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["status"] != float64(http.StatusBadGateway) {
		t.Errorf("status = %v, want 502", rec["status"])
	}
}

func TestClient_NoLoggerByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	// No WithLogger: must not panic and must default to discarding logs.
	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.SearchByISRC(context.Background(), "x"); err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}
}

// decodeLog parses the single JSON log record written to buf.
func decodeLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	return rec
}

func TestClient_PropagatesTokenError(t *testing.T) {
	c := NewClient(errToken{}, WithBaseURL("http://example.invalid"))
	if _, err := c.SearchByISRC(context.Background(), "x"); err == nil {
		t.Fatal("expected error when token provider fails")
	}
}

func TestClient_RateLimitSurfacesTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.SearchByISRC(context.Background(), "x")
	var rl *httpx.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *httpx.RateLimitError, got %T (%v)", err, err)
	}
}

func TestClient_ServerErrorSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.SearchByISRC(context.Background(), "x")
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *httpx.APIError, got %T (%v)", err, err)
	}
}
