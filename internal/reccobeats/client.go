// Package reccobeats provides a typed client for the ReccoBeats audio-features
// API, used to recover a track's audio characteristics from a Spotify track ID.
//
// ReccoBeats is used because Spotify's own /audio-features endpoint was
// deprecated on 2024-11-27 and returns 403 for new apps. The API is free and
// requires no API key.
package reccobeats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

// DefaultBaseURL is the base URL of the ReccoBeats API.
const DefaultBaseURL = "https://api.reccobeats.com"

// serviceName labels errors originating from ReccoBeats.
const serviceName = "reccobeats"

// ErrNotFound is returned when ReccoBeats has no audio features for the given
// Spotify ID. The endpoint signals this with a 200 and an empty content array,
// not a 404, so callers should test for this with errors.Is rather than a
// status code.
var ErrNotFound = errors.New("reccobeats: no audio features for track")

// AudioFeatures holds a track's musical characteristics. The field set mirrors
// Spotify's classic audio-features schema, which ReccoBeats reproduces.
type AudioFeatures struct {
	Acousticness     float64 `json:"acousticness"`
	Danceability     float64 `json:"danceability"`
	Energy           float64 `json:"energy"`
	Instrumentalness float64 `json:"instrumentalness"`
	Liveness         float64 `json:"liveness"`
	Loudness         float64 `json:"loudness"`
	Speechiness      float64 `json:"speechiness"`
	Tempo            float64 `json:"tempo"`
	Valence          float64 `json:"valence"`
	Key              int     `json:"key"`
	Mode             int     `json:"mode"`
}

// Client is a typed wrapper around the ReccoBeats audio-features endpoint.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the ReccoBeats base URL (useful for tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client used for API calls.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithLogger sets the logger used to record outbound requests. Without it, the
// client discards request logs.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.logger = l }
}

// NewClient builds a ReccoBeats client. No credentials are required.
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		httpClient: http.DefaultClient,
		logger:     slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// audioFeaturesResponse wraps the ReccoBeats /v1/audio-features payload, which
// returns a content array (the endpoint is batch-oriented even for one ID).
type audioFeaturesResponse struct {
	Content []AudioFeatures `json:"content"`
}

// AudioFeatures fetches the audio features for a Spotify track ID. It calls the
// batch /v1/audio-features endpoint, which accepts Spotify IDs directly — unlike
// the single-track /track/{id} path, which requires a ReccoBeats UUID. When the
// track is unknown to ReccoBeats the endpoint returns 200 with empty content,
// which is surfaced as ErrNotFound.
func (c *Client) AudioFeatures(ctx context.Context, spotifyID string) (*AudioFeatures, error) {
	endpoint := c.baseURL + "/v1/audio-features?ids=" + url.QueryEscape(spotifyID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("reccobeats: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("reccobeats request",
			"spotify_id", spotifyID,
			"dur", time.Since(start).String(),
			"err", err.Error(),
		)
		return nil, fmt.Errorf("reccobeats: request: %w", err)
	}
	defer resp.Body.Close()

	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		c.logger.Error("reccobeats request",
			"spotify_id", spotifyID,
			"status", resp.StatusCode,
			"dur", time.Since(start).String(),
			"err", err.Error(),
		)
		return nil, err
	}

	var body audioFeaturesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("reccobeats: decode response: %w", err)
	}

	found := len(body.Content) > 0
	c.logger.Info("reccobeats request",
		"spotify_id", spotifyID,
		"status", resp.StatusCode,
		"found", found,
		"dur", time.Since(start).String(),
	)
	if !found {
		return nil, ErrNotFound
	}
	return &body.Content[0], nil
}
