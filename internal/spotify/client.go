package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

// DefaultAPIBaseURL is the base URL of the Spotify Web API.
const DefaultAPIBaseURL = "https://api.spotify.com/v1"

// serviceName labels errors originating from Spotify.
const serviceName = "spotify"

// TokenProvider supplies a valid Spotify bearer access token, refreshing it as
// needed. The search client depends on this interface rather than a concrete
// type; spotifyauth.ClientCredentials satisfies it.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// Track is a parsed Spotify search result. Only the fields the backend needs are
// surfaced; the Spotify track ID is the value the rest of the pipeline depends on.
type Track struct {
	ID      string
	Name    string
	Artists []string
	ISRC    string
}

// Client is a typed wrapper around the Spotify Web API Search endpoint. It
// injects the bearer token from a TokenProvider on every request and translates
// upstream failures into the structured errors in package httpx.
type Client struct {
	tokens     TokenProvider
	baseURL    string
	httpClient *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithBaseURL overrides the Spotify API base URL (useful for tests).
func WithBaseURL(u string) ClientOption {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client used for API calls.
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = h }
}

// NewClient builds a Spotify search client backed by the given token provider.
func NewClient(tokens TokenProvider, opts ...ClientOption) *Client {
	c := &Client{
		tokens:     tokens,
		baseURL:    DefaultAPIBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SearchByISRC resolves an ISRC to candidate tracks. An ISRC identifies a single
// recording, so callers typically use the first result's ID.
func (c *Client) SearchByISRC(ctx context.Context, isrc string) ([]Track, error) {
	return c.search(ctx, "isrc:"+isrc)
}

// SearchByTitleArtist searches by free-text title and artist, used as a fallback
// when an ISRC lookup yields nothing.
func (c *Client) SearchByTitleArtist(ctx context.Context, title, artist string) ([]Track, error) {
	return c.search(ctx, fmt.Sprintf("track:%s artist:%s", title, artist))
}

// searchResponse models the subset of the Spotify search payload we consume.
type searchResponse struct {
	Tracks struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			ExternalIDs struct {
				ISRC string `json:"isrc"`
			} `json:"external_ids"`
		} `json:"items"`
	} `json:"tracks"`
}

func (c *Client) search(ctx context.Context, query string) ([]Track, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("spotify: obtain token: %w", err)
	}

	params := url.Values{
		"type":  {"track"},
		"q":     {query},
		"limit": {"10"},
	}
	endpoint := c.baseURL + "/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: build search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify: search request: %w", err)
	}
	defer resp.Body.Close()

	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		return nil, err
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("spotify: decode search response: %w", err)
	}

	tracks := make([]Track, 0, len(sr.Tracks.Items))
	for _, it := range sr.Tracks.Items {
		artists := make([]string, 0, len(it.Artists))
		for _, a := range it.Artists {
			artists = append(artists, a.Name)
		}
		tracks = append(tracks, Track{
			ID:      it.ID,
			Name:    it.Name,
			Artists: artists,
			ISRC:    it.ExternalIDs.ISRC,
		})
	}
	return tracks, nil
}
