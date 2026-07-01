package albumimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/albumimport"
	"github.com/aaronpollock/liner-notes-server/internal/ingest"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
)

type fakeFetcher struct {
	tracks []spotify.Track
	err    error
}

func (f *fakeFetcher) AlbumTracks(_ context.Context, _, _ string) ([]spotify.Track, error) {
	return f.tracks, f.err
}

type fakeIngester struct {
	calledWith []ingest.Item
	summary    ingest.Summary
}

func (f *fakeIngester) IngestList(_ context.Context, items []ingest.Item) ingest.Summary {
	f.calledWith = items
	return f.summary
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/album/import", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	return w
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{}, &fakeIngester{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/album/import", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{}, &fakeIngester{})
	w := post(t, h, `{bad json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_MissingArtist(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{}, &fakeIngester{})
	w := post(t, h, `{"album":"Moon Safari"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_MissingAlbum(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{}, &fakeIngester{})
	w := post(t, h, `{"artist":"Air"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_FetcherError(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{err: errors.New("spotify down")}, &fakeIngester{})
	w := post(t, h, `{"artist":"Air","album":"Moon Safari"}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHandler_NoTracksFound(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{tracks: nil}, &fakeIngester{})
	w := post(t, h, `{"artist":"Air","album":"Nonexistent"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]int
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ingested"] != 0 {
		t.Errorf("ingested = %d, want 0", resp["ingested"])
	}
}

func TestHandler_PassesTracksToIngester(t *testing.T) {
	ing := &fakeIngester{summary: ingest.Summary{Ingested: 2}}
	tracks := []spotify.Track{
		{ID: "t1", Name: "La femme d'argent", Artists: []string{"Air"}},
		{ID: "t2", Name: "Sexy Boy", Artists: []string{"Air"}},
	}
	h := albumimport.NewHandler(&fakeFetcher{tracks: tracks}, ing)
	w := post(t, h, `{"artist":"Air","album":"Moon Safari"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(ing.calledWith) != 2 {
		t.Fatalf("ingester called with %d items, want 2", len(ing.calledWith))
	}
	if ing.calledWith[0].Title != "La femme d'argent" {
		t.Errorf("item[0].Title = %q", ing.calledWith[0].Title)
	}
	if ing.calledWith[0].Artist != "Air" {
		t.Errorf("item[0].Artist = %q", ing.calledWith[0].Artist)
	}
	if ing.calledWith[0].Source != "album_import" {
		t.Errorf("item[0].Source = %q, want album_import", ing.calledWith[0].Source)
	}
}

func TestHandler_ReturnsSummary(t *testing.T) {
	ing := &fakeIngester{summary: ingest.Summary{Ingested: 8, Unresolved: 1, NoFeatures: 1, Errored: 0}}
	tracks := make([]spotify.Track, 10)
	for i := range tracks {
		tracks[i] = spotify.Track{Name: "Track", Artists: []string{"Artist"}}
	}
	h := albumimport.NewHandler(&fakeFetcher{tracks: tracks}, ing)
	w := post(t, h, `{"artist":"Artist","album":"Album"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Ingested   int `json:"ingested"`
		Unresolved int `json:"unresolved"`
		NoFeatures int `json:"no_features"`
		Errored    int `json:"errored"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Ingested != 8 {
		t.Errorf("ingested = %d, want 8", resp.Ingested)
	}
	if resp.Unresolved != 1 {
		t.Errorf("unresolved = %d, want 1", resp.Unresolved)
	}
}

func TestHandler_UsesFirstArtistFromTrack(t *testing.T) {
	ing := &fakeIngester{}
	tracks := []spotify.Track{
		{Name: "Song", Artists: []string{"Featured Artist", "Main Artist"}},
	}
	h := albumimport.NewHandler(&fakeFetcher{tracks: tracks}, ing)
	post(t, h, `{"artist":"Main Artist","album":"Album"}`)
	if len(ing.calledWith) != 1 {
		t.Fatalf("expected 1 item, got %d", len(ing.calledWith))
	}
	if ing.calledWith[0].Artist != "Featured Artist" {
		t.Errorf("Artist = %q, want Featured Artist (first artist on track)", ing.calledWith[0].Artist)
	}
}

func TestHandler_ResponseContentType(t *testing.T) {
	h := albumimport.NewHandler(&fakeFetcher{}, &fakeIngester{})
	w := post(t, h, `{"artist":"Air","album":"Moon Safari"}`)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHandler_UsesFirstSpotifyTrackArtistNotRequestArtist(t *testing.T) {
	ing := &fakeIngester{}
	tracks := []spotify.Track{{Name: "Collabs", Artists: []string{"Guest"}}}
	h := albumimport.NewHandler(&fakeFetcher{tracks: tracks}, ing)

	body, _ := json.Marshal(map[string]string{"artist": "Host", "album": "Record"})
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if ing.calledWith[0].Artist != "Guest" {
		t.Errorf("Artist = %q, want Guest (from Spotify track, not request body)", ing.calledWith[0].Artist)
	}
}
