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

func TestClient_ExtractsLargestAlbumArt(t *testing.T) {
	const withArt = `{
	  "tracks": {
	    "items": [
	      {
	        "id": "track-abc",
	        "name": "Song",
	        "artists": [{"name": "Artist"}],
	        "external_ids": {"isrc": "X"},
	        "album": {
	          "images": [
	            {"url": "https://i.scdn.co/medium", "width": 300, "height": 300},
	            {"url": "https://i.scdn.co/large",  "width": 640, "height": 640},
	            {"url": "https://i.scdn.co/small",  "width": 64,  "height": 64}
	          ]
	        }
	      }
	    ]
	  }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(withArt))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "X")
	if err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if got := tracks[0].AlbumArtURL; got != "https://i.scdn.co/large" {
		t.Errorf("AlbumArtURL = %q, want the widest image", got)
	}
}

func TestClient_ExtractsDuration(t *testing.T) {
	const withDuration = `{
	  "tracks": {
	    "items": [
	      {
	        "id": "track-abc",
	        "name": "Song",
	        "artists": [{"name": "Artist"}],
	        "external_ids": {"isrc": "X"},
	        "duration_ms": 215000
	      }
	    ]
	  }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(withDuration))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "X")
	if err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if got := tracks[0].DurationMs; got != 215000 {
		t.Errorf("DurationMs = %d, want 215000", got)
	}
}

func TestClient_NoDuration(t *testing.T) {
	// searchResponseJSON has no duration_ms — must default to 0, not panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "USSUB0500001")
	if err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}
	if tracks[0].DurationMs != 0 {
		t.Errorf("DurationMs = %d, want 0", tracks[0].DurationMs)
	}
}

func TestClient_NoAlbumArt(t *testing.T) {
	// searchResponseJSON has no album/images — must not panic, empty art.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchResponseJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.SearchByISRC(context.Background(), "USSUB0500001")
	if err != nil {
		t.Fatalf("SearchByISRC error: %v", err)
	}
	if tracks[0].AlbumArtURL != "" {
		t.Errorf("AlbumArtURL = %q, want empty", tracks[0].AlbumArtURL)
	}
}

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

// — AlbumTracks —

const albumSearchJSON = `{
  "albums": {
    "items": [{"id": "album-123", "name": "Moon Safari", "artists": [{"name": "Air"}]}]
  }
}`

const albumTracksJSON = `{
  "items": [
    {"id": "track-1", "name": "La femme d'argent", "artists": [{"name": "Air"}], "track_number": 1, "duration_ms": 429000},
    {"id": "track-2", "name": "Sexy Boy",           "artists": [{"name": "Air"}], "track_number": 2, "duration_ms": 300000}
  ]
}`

func albumTestServer(t *testing.T, albumResp, tracksResp string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/search" {
			if got := r.URL.Query().Get("type"); got != "album" {
				t.Errorf("AlbumTracks search: type = %q, want album", got)
			}
			_, _ = w.Write([]byte(albumResp))
		} else {
			_, _ = w.Write([]byte(tracksResp))
		}
	}))
}

func TestClient_AlbumTracks_ReturnsTracks(t *testing.T) {
	srv := albumTestServer(t, albumSearchJSON, albumTracksJSON)
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.AlbumTracks(context.Background(), "Moon Safari", "Air")
	if err != nil {
		t.Fatalf("AlbumTracks error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Name != "La femme d'argent" {
		t.Errorf("tracks[0].Name = %q", tracks[0].Name)
	}
	if tracks[0].ID != "track-1" {
		t.Errorf("tracks[0].ID = %q", tracks[0].ID)
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Air" {
		t.Errorf("tracks[0].Artists = %v", tracks[0].Artists)
	}
	if tracks[0].DurationMs != 429000 {
		t.Errorf("tracks[0].DurationMs = %d", tracks[0].DurationMs)
	}
}

func TestClient_AlbumTracks_SearchQueryIncludesAlbumAndArtist(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/search" {
			gotQuery = r.URL.Query().Get("q")
			_, _ = w.Write([]byte(albumSearchJSON))
		} else {
			_, _ = w.Write([]byte(albumTracksJSON))
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.AlbumTracks(context.Background(), "Moon Safari", "Air"); err != nil {
		t.Fatalf("AlbumTracks error: %v", err)
	}
	if gotQuery != "album:Moon Safari artist:Air" {
		t.Errorf("q = %q, want album:Moon Safari artist:Air", gotQuery)
	}
}

func TestClient_AlbumTracks_NoAlbumFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"albums": {"items": []}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.AlbumTracks(context.Background(), "Nonexistent", "Nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestClient_AlbumTracks_SearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.AlbumTracks(context.Background(), "Album", "Artist"); err == nil {
		t.Fatal("expected error when album search fails")
	}
}

func TestClient_AlbumTracks_FallsBackToAlbumArtistWhenTrackHasNone(t *testing.T) {
	const noArtistTracks = `{"items": [{"id": "t1", "name": "Intro", "artists": [], "track_number": 1, "duration_ms": 60000}]}`
	srv := albumTestServer(t, albumSearchJSON, noArtistTracks)
	t.Cleanup(srv.Close)

	c := NewClient(staticToken("t"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	tracks, err := c.AlbumTracks(context.Background(), "Moon Safari", "Air")
	if err != nil {
		t.Fatalf("AlbumTracks error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Air" {
		t.Errorf("expected fallback artist Air, got %v", tracks[0].Artists)
	}
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
