// Package lookup implements the Liner Notes pipeline: it resolves an ISRC (with a
// title/artist fallback) to a Spotify track ID, fetches the track's audio
// features, and degrades gracefully when the upstreams — which have no SLA —
// misbehave, so a scan never hard-fails on Spotify/ReccoBeats trouble.
package lookup

import (
	"context"
	"errors"
	"strings"

	"github.com/aaronpollock/liner-notes-server/internal/reccobeats"
	"github.com/aaronpollock/liner-notes-server/internal/retry"
	"github.com/aaronpollock/liner-notes-server/internal/spotify"
)

// ErrInvalidRequest indicates the caller supplied no ISRC to resolve.
var ErrInvalidRequest = errors.New("lookup: request requires an ISRC")

// FeaturesStatus reports whether audio features were resolved for a scan.
type FeaturesStatus string

const (
	StatusAvailable   FeaturesStatus = "available"
	StatusUnavailable FeaturesStatus = "unavailable"
)

// Request is a lookup request, mirroring what the mobile client knows after a
// ShazamKit match: an ISRC plus optional human-readable metadata.
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

// IDCache caches the immutable ISRC → Spotify-ID mapping. cache.Memory satisfies it.
type IDCache interface {
	Get(isrc string) (string, bool)
	Set(isrc, spotifyID string)
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
	if req.ISRC == "" {
		return Result{}, ErrInvalidRequest
	}

	result := Result{
		ISRC:           req.ISRC,
		Title:          req.Title,
		Artist:         req.Artist,
		FeaturesStatus: StatusUnavailable,
	}

	spotifyID := s.resolveSpotifyID(ctx, req, &result)
	if spotifyID == "" {
		// Could not resolve the track at all; return Shazam metadata as-is.
		return result, nil
	}
	result.SpotifyID = spotifyID

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

// resolveSpotifyID returns the Spotify track ID for the request, consulting the
// cache first, then an ISRC search, then a title/artist fallback. It returns ""
// when the track cannot be resolved. Resolved metadata enriches result.
func (s *Service) resolveSpotifyID(ctx context.Context, req Request, result *Result) string {
	if id, ok := s.cache.Get(req.ISRC); ok {
		return id
	}

	// Primary: resolve by ISRC, which identifies the exact recording.
	if tracks := s.searchTracks(ctx, func(ctx context.Context) ([]spotify.Track, error) {
		return s.search.SearchByISRC(ctx, req.ISRC)
	}); len(tracks) > 0 {
		return s.acceptTrack(req.ISRC, tracks[0], result)
	}

	// Fallback: free-text title + artist from the Shazam match.
	if req.Title != "" && req.Artist != "" {
		if tracks := s.searchTracks(ctx, func(ctx context.Context) ([]spotify.Track, error) {
			return s.search.SearchByTitleArtist(ctx, req.Title, req.Artist)
		}); len(tracks) > 0 {
			return s.acceptTrack(req.ISRC, tracks[0], result)
		}
	}

	return ""
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

// acceptTrack records the ISRC → ID mapping, enriches missing metadata from the
// resolved track, and returns the track ID.
func (s *Service) acceptTrack(isrc string, t spotify.Track, result *Result) string {
	s.cache.Set(isrc, t.ID)
	if result.Title == "" {
		result.Title = t.Name
	}
	if result.Artist == "" && len(t.Artists) > 0 {
		result.Artist = strings.Join(t.Artists, ", ")
	}
	return t.ID
}
