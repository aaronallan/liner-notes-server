// Package lookup implements the Liner Notes pipeline: it resolves a track from
// its title/artist (with an optional ISRC to sharpen the match) to a Spotify
// track ID, fetches the track's audio features, and degrades gracefully when the
// upstreams — which have no SLA — misbehave, so a scan never hard-fails on
// Spotify/ReccoBeats trouble.
package lookup

import (
	"context"
	"errors"
	"strings"

	"github.com/aaronpollock/liner-notes-server/internal/normalize"
	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/retry"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
)

// ErrInvalidRequest indicates the caller supplied no usable title+artist pair to
// resolve the track from.
var ErrInvalidRequest = errors.New("lookup: request requires a title and artist")

// FeaturesStatus reports whether audio features were resolved for a scan.
type FeaturesStatus string

const (
	StatusAvailable   FeaturesStatus = "available"
	StatusUnavailable FeaturesStatus = "unavailable"
)

// Request is a lookup request, mirroring what the mobile client knows after a
// ShazamKit match: a title and artist, plus an optional ISRC. In practice
// ShazamKit rarely supplies an ISRC, so title+artist is the primary input; an
// ISRC, when present, is used opportunistically to pin the exact recording.
type Request struct {
	ISRC   string
	Title  string
	Artist string
}

// Result is the outcome of a lookup. When FeaturesStatus is StatusUnavailable,
// Features is nil but the metadata fields still carry whatever the scan knew.
type Result struct {
	ISRC           string
	Title          string
	Artist         string
	SpotifyID      string
	AlbumArtURL    string
	DurationMs     int
	Features       *reccobeats.AudioFeatures
	FeaturesStatus FeaturesStatus
}

// Searcher resolves tracks via the Spotify Search API. spotify.Client satisfies
// it; the interface is defined here so the service can be tested with fakes.
type Searcher interface {
	SearchByISRC(ctx context.Context, isrc string) ([]spotify.Track, error)
	SearchByTitleArtist(ctx context.Context, title, artist string) ([]spotify.Track, error)
}

// FeaturesFetcher fetches audio features for a Spotify track ID. reccobeats.Client
// satisfies it.
type FeaturesFetcher interface {
	AudioFeatures(ctx context.Context, spotifyID string) (*reccobeats.AudioFeatures, error)
}

// IDCache caches the (normalized title+artist) → Spotify-ID mapping. The key is
// an identifier mapping derived from the caller's own match metadata, so it is
// safe to cache. cache.Memory satisfies it.
type IDCache interface {
	Get(key string) (string, bool)
	Set(key, spotifyID string)
}

// Service ties the Spotify and ReccoBeats clients into the full pipeline.
type Service struct {
	search   Searcher
	features FeaturesFetcher
	cache    IDCache
	retry    retry.Config
}

// Option configures a Service.
type Option func(*Service)

// WithRetry overrides the retry/backoff schedule for upstream calls.
func WithRetry(cfg retry.Config) Option {
	return func(s *Service) { s.retry = cfg }
}

// NewService builds the lookup service from its collaborators.
func NewService(search Searcher, features FeaturesFetcher, cache IDCache, opts ...Option) *Service {
	s := &Service{
		search:   search,
		features: features,
		cache:    cache,
		retry:    retry.DefaultConfig(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Lookup runs the pipeline for one request. It only returns an error for invalid
// input; upstream failures are absorbed into a degraded (but successful) result.
func (s *Service) Lookup(ctx context.Context, req Request) (Result, error) {
	// Title+artist is the contract; an ISRC alone is not enough to key on.
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Artist) == "" {
		return Result{}, ErrInvalidRequest
	}

	result := Result{
		ISRC:           req.ISRC,
		Title:          req.Title,
		Artist:         req.Artist,
		FeaturesStatus: StatusUnavailable,
	}

	spotifyID, albumArt, durationMs := s.resolveSpotifyID(ctx, req)
	if spotifyID == "" {
		// Could not resolve the track at all; return the scan metadata as-is.
		return result, nil
	}
	result.SpotifyID = spotifyID
	// Album art and duration are Spotify content: forwarded for immediate use,
	// never stored. They are only available on a fresh search, not a cache hit.
	result.AlbumArtURL = albumArt
	result.DurationMs = durationMs

	feats, err := retry.Do(ctx, s.retry, func(ctx context.Context) (*reccobeats.AudioFeatures, error) {
		return s.features.AudioFeatures(ctx, spotifyID)
	})
	if err != nil {
		// Features could not be fetched; degrade rather than fail the scan.
		return result, nil
	}
	result.Features = feats
	result.FeaturesStatus = StatusAvailable
	return result, nil
}

// resolveSpotifyID returns the Spotify track ID for the request (plus the album
// art URL when it comes from a fresh search): the cache (keyed on normalized
// title+artist) first, then — if an ISRC is present — an ISRC search for
// accuracy, then a title/artist search. It returns "" for the ID when the track
// cannot be resolved. A cache hit yields no album art, since only the identifier
// is cached.
func (s *Service) resolveSpotifyID(ctx context.Context, req Request) (id, albumArt string, durationMs int) {
	key := normalize.Key(req.Title, req.Artist)
	if cached, ok := s.cache.Get(key); ok {
		return cached, "", 0
	}

	// Opportunistic: an ISRC, when present, pins the exact recording.
	if req.ISRC != "" {
		if tracks := s.searchTracks(ctx, func(ctx context.Context) ([]spotify.Track, error) {
			return s.search.SearchByISRC(ctx, req.ISRC)
		}); len(tracks) > 0 {
			return s.acceptTrack(key, tracks[0]), tracks[0].AlbumArtURL, tracks[0].DurationMs
		}
	}

	// Primary: free-text title + artist from the match.
	if tracks := s.searchTracks(ctx, func(ctx context.Context) ([]spotify.Track, error) {
		return s.search.SearchByTitleArtist(ctx, req.Title, req.Artist)
	}); len(tracks) > 0 {
		return s.acceptTrack(key, tracks[0]), tracks[0].AlbumArtURL, tracks[0].DurationMs
	}

	return "", "", 0
}

// searchTracks runs a search with retry, swallowing errors (best-effort): an
// upstream failure is treated the same as "no match" so the scan can degrade.
func (s *Service) searchTracks(ctx context.Context, fn func(context.Context) ([]spotify.Track, error)) []spotify.Track {
	tracks, err := retry.Do(ctx, s.retry, fn)
	if err != nil {
		return nil
	}
	return tracks
}

// acceptTrack records the (normalized title+artist) → ID mapping and returns the
// track ID. It never stores Spotify-sourced metadata — only the identifier.
func (s *Service) acceptTrack(key string, t spotify.Track) string {
	s.cache.Set(key, t.ID)
	return t.ID
}
