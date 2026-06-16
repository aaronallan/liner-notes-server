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
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

// DefaultBaseURL is the base URL of the ReccoBeats API.
const DefaultBaseURL = "https://api.reccobeats.com"

// serviceName labels errors originating from ReccoBeats.
const serviceName = "reccobeats"

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

// NewClient builds a ReccoBeats client. No credentials are required.
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AudioFeatures fetches the audio features for a Spotify track ID.
func (c *Client) AudioFeatures(ctx context.Context, spotifyID string) (*AudioFeatures, error) {
	endpoint := fmt.Sprintf("%s/v1/track/%s/audio-features", c.baseURL, url.PathEscape(spotifyID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("reccobeats: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reccobeats: request: %w", err)
	}
	defer resp.Body.Close()

	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		return nil, err
	}

	var feats AudioFeatures
	if err := json.NewDecoder(resp.Body).Decode(&feats); err != nil {
		return nil, fmt.Errorf("reccobeats: decode response: %w", err)
	}
	return &feats, nil
}
