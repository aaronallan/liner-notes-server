package lookup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/cache"
	"github.com/aaronpollock/liner-notes-server/internal/httpx"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/retry"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
)

// fakeSearcher is a programmable Searcher that records call counts.
type fakeSearcher struct {
	byISRC     func(isrc string) ([]spotify.Track, error)
	byTitle    func(title, artist string) ([]spotify.Track, error)
	isrcCalls  int
	titleCalls int
}

func (f *fakeSearcher) SearchByISRC(_ context.Context, isrc string) ([]spotify.Track, error) {
	f.isrcCalls++
	return f.byISRC(isrc)
}

func (f *fakeSearcher) SearchByTitleArtist(_ context.Context, title, artist string) ([]spotify.Track, error) {
	f.titleCalls++
	return f.byTitle(title, artist)
}

// fakeFeatures is a programmable FeaturesFetcher.
type fakeFeatures struct {
	fn    func(id string) (*reccobeats.AudioFeatures, error)
	calls int
}

func (f *fakeFeatures) AudioFeatures(_ context.Context, id string) (*reccobeats.AudioFeatures, error) {
	f.calls++
	return f.fn(id)
}

func track(id, name string, artists ...string) spotify.Track {
	return spotify.Track{ID: id, Name: name, Artists: artists}
}

func newTestService(s Searcher, f FeaturesFetcher) *Service {
	return NewService(s, f, cache.NewMemory[string, string](),
		WithRetry(retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))
}

// req is a valid request: title+artist are required, ISRC is optional.
func req(title, artist string) Request { return Request{Title: title, Artist: artist} }

func TestLookup_HappyPath(t *testing.T) {
	feats := &reccobeats.AudioFeatures{Tempo: 120, Danceability: 0.8}
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			return []spotify.Track{track("track-1", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) { return feats, nil }}

	res, err := newTestService(search, features).Lookup(context.Background(), req("Song", "Artist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SpotifyID != "track-1" {
		t.Errorf("SpotifyID = %q, want track-1", res.SpotifyID)
	}
	if res.FeaturesStatus != StatusAvailable {
		t.Errorf("FeaturesStatus = %q, want available", res.FeaturesStatus)
	}
	if res.Features == nil || res.Features.Tempo != 120 {
		t.Errorf("Features = %+v, want tempo 120", res.Features)
	}
}

func TestLookup_UsesISRCSearchFirstWhenPresent(t *testing.T) {
	search := &fakeSearcher{
		byISRC: func(string) ([]spotify.Track, error) {
			return []spotify.Track{track("isrc-id", "Song", "Artist")}, nil
		},
		byTitle: func(string, string) ([]spotify.Track, error) {
			t.Fatal("title/artist search must not run when the ISRC search hits")
			return nil, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}

	res, err := newTestService(search, features).Lookup(context.Background(),
		Request{ISRC: "ISRC1", Title: "Song", Artist: "Artist"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SpotifyID != "isrc-id" {
		t.Errorf("SpotifyID = %q, want isrc-id", res.SpotifyID)
	}
	if search.isrcCalls != 1 || search.titleCalls != 0 {
		t.Errorf("isrcCalls=%d titleCalls=%d, want 1/0", search.isrcCalls, search.titleCalls)
	}
}

func TestLookup_FallsBackToTitleArtistWhenISRCMisses(t *testing.T) {
	search := &fakeSearcher{
		byISRC: func(string) ([]spotify.Track, error) { return nil, nil }, // ISRC yields nothing
		byTitle: func(title, artist string) ([]spotify.Track, error) {
			if title != "Song" || artist != "Artist" {
				t.Errorf("fallback called with %q/%q", title, artist)
			}
			return []spotify.Track{track("fallback-id", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}

	res, err := newTestService(search, features).Lookup(context.Background(),
		Request{ISRC: "ISRC1", Title: "Song", Artist: "Artist"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SpotifyID != "fallback-id" {
		t.Errorf("SpotifyID = %q, want fallback-id", res.SpotifyID)
	}
	if search.isrcCalls != 1 || search.titleCalls != 1 {
		t.Errorf("isrcCalls=%d titleCalls=%d, want 1/1", search.isrcCalls, search.titleCalls)
	}
}

func TestLookup_ReturnsAlbumArtFromSearch(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			return []spotify.Track{{ID: "track-1", Name: "Song", Artists: []string{"Artist"}, AlbumArtURL: "https://i.scdn.co/large"}}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}

	res, err := newTestService(search, features).Lookup(context.Background(), req("Song", "Artist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AlbumArtURL != "https://i.scdn.co/large" {
		t.Errorf("AlbumArtURL = %q, want forwarded from the resolved track", res.AlbumArtURL)
	}
}

func TestLookup_CachesByTitleArtist(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			return []spotify.Track{track("track-1", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}
	svc := newTestService(search, features)

	for i := range 3 {
		if _, err := svc.Lookup(context.Background(), req("Song", "Artist")); err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
	}
	if search.titleCalls != 1 {
		t.Errorf("titleCalls = %d, want 1 (mapping should be cached)", search.titleCalls)
	}
}

func TestLookup_CacheKeyCaseAndWhitespaceInsensitive(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			return []spotify.Track{track("track-1", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}
	svc := newTestService(search, features)

	_, _ = svc.Lookup(context.Background(), req("Song", "Artist"))
	_, _ = svc.Lookup(context.Background(), req("  song ", "ARTIST"))

	if search.titleCalls != 1 {
		t.Errorf("titleCalls = %d, want 1 (case/whitespace variant should hit cache)", search.titleCalls)
	}
}

func TestLookup_VersionQualifiersDoNotCollide(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(title, _ string) ([]spotify.Track, error) {
			return []spotify.Track{track("id:"+title, title, "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}
	svc := newTestService(search, features)

	r1, _ := svc.Lookup(context.Background(), req("Song", "Artist"))
	r2, _ := svc.Lookup(context.Background(), req("Song (Live)", "Artist"))

	if search.titleCalls != 2 {
		t.Errorf("titleCalls = %d, want 2 (distinct recordings must not share a cache entry)", search.titleCalls)
	}
	if r1.SpotifyID == r2.SpotifyID {
		t.Errorf("distinct titles resolved to the same id: %q", r1.SpotifyID)
	}
}

func TestLookup_RequiresTitleAndArtist(t *testing.T) {
	svc := newTestService(&fakeSearcher{}, &fakeFeatures{})

	cases := []Request{
		{},                             // nothing
		{Title: "Song"},                // missing artist
		{Artist: "Artist"},             // missing title
		{ISRC: "ISRC1"},                // ISRC alone is not enough
		{ISRC: "ISRC1", Title: "Song"}, // ISRC + title, still no artist
		{Title: "  ", Artist: "  "},    // whitespace only
	}
	for _, c := range cases {
		if _, err := svc.Lookup(context.Background(), c); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("Lookup(%+v) error = %v, want ErrInvalidRequest", c, err)
		}
	}
}

func TestLookup_DegradesWhenNoTrackResolved(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) { return nil, nil },
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		t.Fatal("features should not be fetched when no ID resolved")
		return nil, nil
	}}

	res, err := newTestService(search, features).Lookup(context.Background(), req("Song", "Artist"))
	if err != nil {
		t.Fatalf("scan must not hard-fail: %v", err)
	}
	if res.SpotifyID != "" {
		t.Errorf("SpotifyID = %q, want empty", res.SpotifyID)
	}
	if res.FeaturesStatus != StatusUnavailable {
		t.Errorf("FeaturesStatus = %q, want unavailable", res.FeaturesStatus)
	}
	if res.Title != "Song" || res.Artist != "Artist" {
		t.Errorf("metadata lost: title=%q artist=%q", res.Title, res.Artist)
	}
}

func TestLookup_DegradesWhenFeaturesUnavailable(t *testing.T) {
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			return []spotify.Track{track("track-1", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return nil, &httpx.APIError{Service: "reccobeats", StatusCode: 404}
	}}

	res, err := newTestService(search, features).Lookup(context.Background(), req("Song", "Artist"))
	if err != nil {
		t.Fatalf("scan must not hard-fail on feature trouble: %v", err)
	}
	if res.SpotifyID != "track-1" {
		t.Errorf("SpotifyID = %q, want track-1", res.SpotifyID)
	}
	if res.Features != nil {
		t.Errorf("Features = %+v, want nil", res.Features)
	}
	if res.FeaturesStatus != StatusUnavailable {
		t.Errorf("FeaturesStatus = %q, want unavailable", res.FeaturesStatus)
	}
}

func TestLookup_RetriesTransientSearchError(t *testing.T) {
	attempts := 0
	search := &fakeSearcher{
		byTitle: func(string, string) ([]spotify.Track, error) {
			attempts++
			if attempts < 2 {
				return nil, &httpx.APIError{Service: "spotify", StatusCode: 503}
			}
			return []spotify.Track{track("track-1", "Song", "Artist")}, nil
		},
	}
	features := &fakeFeatures{fn: func(string) (*reccobeats.AudioFeatures, error) {
		return &reccobeats.AudioFeatures{}, nil
	}}

	res, err := newTestService(search, features).Lookup(context.Background(), req("Song", "Artist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SpotifyID != "track-1" {
		t.Errorf("SpotifyID = %q, want track-1 after retry", res.SpotifyID)
	}
	if attempts != 2 {
		t.Errorf("search attempts = %d, want 2", attempts)
	}
}
