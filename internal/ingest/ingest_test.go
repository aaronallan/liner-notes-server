package ingest

import (
	"context"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/lookup"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/store"
)

// fakeLooker returns a programmed result per (title) for assertion convenience.
type fakeLooker struct {
	fn func(lookup.Request) (lookup.Result, error)
}

func (f fakeLooker) Lookup(_ context.Context, req lookup.Request) (lookup.Result, error) {
	return f.fn(req)
}

// fakeCorpus records upserts.
type fakeCorpus struct {
	tracks   []store.Track
	features map[string]store.MixFeatures
}

func newFakeCorpus() *fakeCorpus { return &fakeCorpus{features: map[string]store.MixFeatures{}} }

func (c *fakeCorpus) UpsertTrack(_ context.Context, t store.Track) (string, error) {
	c.tracks = append(c.tracks, t)
	return "id-" + t.SpotifyID, nil
}

func (c *fakeCorpus) UpsertMixFeatures(_ context.Context, spotifyID string, f store.MixFeatures) error {
	c.features[spotifyID] = f
	return nil
}

func availableResult(spotifyID string, f *reccobeats.AudioFeatures) lookup.Result {
	return lookup.Result{SpotifyID: spotifyID, Features: f, FeaturesStatus: lookup.StatusAvailable}
}

func TestIngestOne_StoresFeatures(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		// key=9, mode=0 -> Camelot 8A
		return availableResult("sp1", &reccobeats.AudioFeatures{Tempo: 120, Key: 9, Mode: 0, Loudness: -5}), nil
	}}
	corpus := newFakeCorpus()

	out, err := New(looker, corpus).IngestOne(context.Background(),
		Item{Title: "Song", Artist: "Artist", Source: "playlist"})
	if err != nil {
		t.Fatalf("IngestOne: %v", err)
	}
	if out != Ingested {
		t.Errorf("outcome = %q, want ingested", out)
	}
	if len(corpus.tracks) != 1 || corpus.tracks[0].SpotifyID != "sp1" {
		t.Fatalf("track not upserted: %+v", corpus.tracks)
	}
	f, ok := corpus.features["sp1"]
	if !ok || f.Tempo != 120 || f.Key != 9 {
		t.Errorf("features not upserted correctly: %+v", f)
	}
}

func TestIngestOne_UsesIngestionTitleArtist(t *testing.T) {
	// The looker echoes different metadata; the corpus must store the ingestion
	// source's title/artist, never Spotify-derived values (Spotify Terms).
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		r := availableResult("sp1", &reccobeats.AudioFeatures{Tempo: 100, Key: 0, Mode: 1, Loudness: -6})
		r.Title, r.Artist = "Spotify Name", "Spotify Artist"
		return r, nil
	}}
	corpus := newFakeCorpus()

	if _, err := New(looker, corpus).IngestOne(context.Background(),
		Item{Title: "My Title", Artist: "My Artist"}); err != nil {
		t.Fatalf("IngestOne: %v", err)
	}
	if corpus.tracks[0].Title != "My Title" || corpus.tracks[0].Artist != "My Artist" {
		t.Errorf("stored %q/%q, want ingestion source values", corpus.tracks[0].Title, corpus.tracks[0].Artist)
	}
}

func TestIngestOne_Unresolved(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		return lookup.Result{SpotifyID: "", FeaturesStatus: lookup.StatusUnavailable}, nil
	}}
	corpus := newFakeCorpus()

	out, err := New(looker, corpus).IngestOne(context.Background(), Item{Title: "X", Artist: "Y"})
	if err != nil {
		t.Fatalf("IngestOne: %v", err)
	}
	if out != Unresolved {
		t.Errorf("outcome = %q, want unresolved", out)
	}
	if len(corpus.tracks) != 0 {
		t.Errorf("nothing should be stored for an unresolved track")
	}
}

func TestIngestOne_NoFeatures(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		return lookup.Result{SpotifyID: "sp1", FeaturesStatus: lookup.StatusUnavailable}, nil
	}}
	corpus := newFakeCorpus()

	out, err := New(looker, corpus).IngestOne(context.Background(), Item{Title: "X", Artist: "Y"})
	if err != nil {
		t.Fatalf("IngestOne: %v", err)
	}
	if out != NoFeatures {
		t.Errorf("outcome = %q, want no_features", out)
	}
	if len(corpus.tracks) != 0 || len(corpus.features) != 0 {
		t.Errorf("a track with no features must not be written to the corpus")
	}
}

func TestIngestOne_UnmappableKey(t *testing.T) {
	looker := fakeLooker{fn: func(lookup.Request) (lookup.Result, error) {
		// key=-1 means ReccoBeats/Spotify couldn't determine the key.
		return availableResult("sp1", &reccobeats.AudioFeatures{Tempo: 120, Key: -1, Mode: 0, Loudness: -5}), nil
	}}
	corpus := newFakeCorpus()

	out, err := New(looker, corpus).IngestOne(context.Background(), Item{Title: "X", Artist: "Y"})
	if err != nil {
		t.Fatalf("IngestOne: %v", err)
	}
	if out != NoFeatures {
		t.Errorf("outcome = %q, want no_features for an unmappable key", out)
	}
	if len(corpus.tracks) != 0 {
		t.Errorf("a track with an unmappable key must not be written to the corpus")
	}
}

func TestIngestList_SummarizesAndContinues(t *testing.T) {
	looker := fakeLooker{fn: func(req lookup.Request) (lookup.Result, error) {
		switch req.Title {
		case "good":
			return availableResult("sp-"+req.Title, &reccobeats.AudioFeatures{Tempo: 120, Key: 9, Mode: 0, Loudness: -5}), nil
		case "nomatch":
			return lookup.Result{SpotifyID: ""}, nil
		case "nofeat":
			return lookup.Result{SpotifyID: "sp-nofeat", FeaturesStatus: lookup.StatusUnavailable}, nil
		default:
			return lookup.Result{}, lookup.ErrInvalidRequest
		}
	}}
	corpus := newFakeCorpus()

	s := New(looker, corpus).IngestList(context.Background(), []Item{
		{Title: "good", Artist: "A"},
		{Title: "nomatch", Artist: "A"},
		{Title: "nofeat", Artist: "A"},
		{Title: "bad", Artist: ""}, // looker returns ErrInvalidRequest
	})

	if s.Ingested != 1 || s.Unresolved != 1 || s.NoFeatures != 1 || s.Errored != 1 {
		t.Errorf("summary = %+v, want 1/1/1/1", s)
	}
}
