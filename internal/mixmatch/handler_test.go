package mixmatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

type fakeLooker struct {
	fn func(lookup.Request) (lookup.Result, error)
}

func (f fakeLooker) Lookup(_ context.Context, req lookup.Request) (lookup.Result, error) {
	return f.fn(req)
}

type fakeMatcher struct {
	fn func(store.MixSeed, int) ([]store.Match, error)
}

func (f fakeMatcher) MixMatches(_ context.Context, seed store.MixSeed, limit int) ([]store.Match, error) {
	return f.fn(seed, limit)
}

func do(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/mix-matches", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func availableSeed() lookup.Result {
	return lookup.Result{
		SpotifyID:      "seed-id",
		Features:       &reccobeats.AudioFeatures{Tempo: 120, Key: 9, Mode: 0, Loudness: -5},
		FeaturesStatus: lookup.StatusAvailable,
	}
}

func TestHandler_Success(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) { return availableSeed(), nil }}
	matcher := fakeMatcher{fn: func(seed store.MixSeed, limit int) ([]store.Match, error) {
		if seed.SpotifyID != "seed-id" || seed.Tempo != 120 || seed.Key != 9 {
			t.Errorf("seed not built from resolved features: %+v", seed)
		}
		return []store.Match{
			{SpotifyID: "m1", Title: "One", Artist: "A", Camelot: "8A", Tempo: 121, Loudness: -5},
		}, nil
	}}

	rec := do(t, NewHandler(looker, matcher), http.MethodPost, `{"title":"Song","artist":"Artist"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}

	var resp struct {
		Matches []map[string]any `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Matches) != 1 || resp.Matches[0]["spotify_id"] != "m1" {
		t.Errorf("matches = %v, want one m1", resp.Matches)
	}
}

func TestHandler_PassesLimit(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) { return availableSeed(), nil }}
	var gotLimit int
	matcher := fakeMatcher{fn: func(_ store.MixSeed, limit int) ([]store.Match, error) {
		gotLimit = limit
		return nil, nil
	}}

	do(t, NewHandler(looker, matcher), http.MethodPost, `{"title":"Song","artist":"Artist","limit":5}`)
	if gotLimit != 5 {
		t.Errorf("limit = %d, want 5", gotLimit)
	}
}

func TestHandler_UnresolvedSeedReturnsEmpty(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		return lookup.Result{SpotifyID: "", FeaturesStatus: lookup.StatusUnavailable}, nil
	}}
	matcher := fakeMatcher{fn: func(store.MixSeed, int) ([]store.Match, error) {
		t.Fatal("matcher must not run when the seed can't be resolved")
		return nil, nil
	}}

	rec := do(t, NewHandler(looker, matcher), http.MethodPost, `{"title":"Song","artist":"Artist"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Matches []map[string]any `json:"matches"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Matches) != 0 {
		t.Errorf("matches = %v, want empty", resp.Matches)
	}
}

func TestHandler_SeedMissingFeaturesReturnsEmpty(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		return lookup.Result{SpotifyID: "seed-id", FeaturesStatus: lookup.StatusUnavailable}, nil
	}}
	matcher := fakeMatcher{fn: func(store.MixSeed, int) ([]store.Match, error) {
		t.Fatal("matcher must not run without seed features")
		return nil, nil
	}}

	rec := do(t, NewHandler(looker, matcher), http.MethodPost, `{"title":"Song","artist":"Artist"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandler_InvalidRequest(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		return lookup.Result{}, lookup.ErrInvalidRequest
	}}
	matcher := fakeMatcher{fn: func(store.MixSeed, int) ([]store.Match, error) { return nil, nil }}

	rec := do(t, NewHandler(looker, matcher), http.MethodPost, `{"title":"Song"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := NewHandler(
		fakeLooker{fn: func(lookup.Request) (lookup.Result, error) { return availableSeed(), nil }},
		fakeMatcher{fn: func(store.MixSeed, int) ([]store.Match, error) { return nil, nil }},
	)
	if rec := do(t, h, http.MethodGet, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
