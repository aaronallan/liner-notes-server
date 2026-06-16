package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
)

// fakeLooker is a programmable Looker for testing the handler in isolation.
type fakeLooker struct {
	fn func(Request) (Result, error)
}

func (f fakeLooker) Lookup(_ context.Context, req Request) (Result, error) { return f.fn(req) }

func doRequest(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/lookup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler_Success(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) {
		if req.ISRC != "ISRC1" {
			t.Errorf("ISRC = %q, want ISRC1", req.ISRC)
		}
		return Result{
			ISRC:           "ISRC1",
			Title:          "Song",
			Artist:         "Artist",
			SpotifyID:      "track-1",
			Features:       &reccobeats.AudioFeatures{Tempo: 120, Danceability: 0.8},
			FeaturesStatus: StatusAvailable,
		}, nil
	}}

	rec := doRequest(t, NewHandler(looker), http.MethodPost, `{"isrc":"ISRC1","title":"Song","artist":"Artist"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["spotify_id"] != "track-1" {
		t.Errorf("spotify_id = %v, want track-1", resp["spotify_id"])
	}
	if resp["features_status"] != "available" {
		t.Errorf("features_status = %v, want available", resp["features_status"])
	}
	if resp["features"] == nil {
		t.Error("features should be present")
	}
}

func TestHandler_DegradedFeaturesUnavailable(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) {
		return Result{
			ISRC:           "ISRC1",
			Title:          "Song",
			Artist:         "Artist",
			FeaturesStatus: StatusUnavailable,
		}, nil
	}}

	rec := doRequest(t, NewHandler(looker), http.MethodPost, `{"isrc":"ISRC1","title":"Song","artist":"Artist"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded scans still succeed)", rec.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["features_status"] != "unavailable" {
		t.Errorf("features_status = %v, want unavailable", resp["features_status"])
	}
	if resp["features"] != nil {
		t.Errorf("features = %v, want null", resp["features"])
	}
	if resp["title"] != "Song" {
		t.Errorf("title = %v, want Song (metadata preserved)", resp["title"])
	}
}

func TestHandler_TitleArtistOnly(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) {
		if req.ISRC != "" {
			t.Errorf("ISRC = %q, want empty", req.ISRC)
		}
		if req.Title != "Such Great Heights" || req.Artist != "The Postal Service" {
			t.Errorf("title/artist = %q/%q, not forwarded", req.Title, req.Artist)
		}
		return Result{
			Title:          "Such Great Heights",
			Artist:         "The Postal Service",
			SpotifyID:      "title-id",
			Features:       &reccobeats.AudioFeatures{Tempo: 90},
			FeaturesStatus: StatusAvailable,
		}, nil
	}}

	rec := doRequest(t, NewHandler(looker), http.MethodPost,
		`{"title":"Such Great Heights","artist":"The Postal Service"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["spotify_id"] != "title-id" {
		t.Errorf("spotify_id = %v, want title-id", resp["spotify_id"])
	}
}

func TestHandler_EmptyISRC(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) {
		return Result{}, ErrInvalidRequest
	}}
	rec := doRequest(t, NewHandler(looker), http.MethodPost, `{"isrc":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_MalformedJSON(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) {
		t.Fatal("service should not be called for malformed JSON")
		return Result{}, nil
	}}
	rec := doRequest(t, NewHandler(looker), http.MethodPost, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	looker := fakeLooker{fn: func(req Request) (Result, error) { return Result{}, nil }}
	rec := doRequest(t, NewHandler(looker), http.MethodGet, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
