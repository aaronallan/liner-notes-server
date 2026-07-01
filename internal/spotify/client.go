package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	ID          string
	Name        string
	Artists     []string
	ISRC        string
	AlbumArtURL string // largest album cover image; empty when none
	DurationMs  int    // track length in milliseconds; 0 when absent
}

// Client is a typed wrapper around the Spotify Web API Search endpoint. It
// injects the bearer token from a TokenProvider on every request and translates
// upstream failures into the structured errors in package httpx.
type Client struct {
	tokens     TokenProvider
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
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

// WithLogger sets the logger used to record outbound search requests. Without
// it, the client discards request logs.
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = l }
}

// NewClient builds a Spotify search client backed by the given token provider.
func NewClient(tokens TokenProvider, opts ...ClientOption) *Client {
	c := &Client{
		tokens:     tokens,
		baseURL:    DefaultAPIBaseURL,
		httpClient: http.DefaultClient,
		logger:     slog.New(slog.DiscardHandler),
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
			DurationMs  int `json:"duration_ms"`
			ExternalIDs struct {
				ISRC string `json:"isrc"`
			} `json:"external_ids"`
			Album struct {
				Images []image `json:"images"`
			} `json:"album"`
		} `json:"items"`
	} `json:"tracks"`
}

// image is one entry in a Spotify album's images array.
type image struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// largestImageURL returns the URL of the widest image, or "" when there are none.
func largestImageURL(images []image) string {
	best := -1
	url := ""
	for _, img := range images {
		if img.Width > best {
			best = img.Width
			url = img.URL
		}
	}
	return url
}

// albumSearchResponse models the album search payload.
type albumSearchResponse struct {
	Albums struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	} `json:"albums"`
}

// albumTracksResponse models the /albums/{id}/tracks payload.
type albumTracksResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		DurationMs int `json:"duration_ms"`
	} `json:"items"`
}

// AlbumTracks returns the tracks on the first Spotify album that matches
// albumName and artistName. Returns nil with no error when no album is found.
func (c *Client) AlbumTracks(ctx context.Context, albumName, artistName string) ([]Track, error) {
	albumID, err := c.searchAlbumID(ctx, albumName, artistName)
	if err != nil {
		return nil, err
	}
	if albumID == "" {
		return nil, nil
	}
	return c.fetchAlbumTracks(ctx, albumID, artistName)
}

func (c *Client) searchAlbumID(ctx context.Context, albumName, artistName string) (string, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("spotify: obtain token: %w", err)
	}
	params := url.Values{
		"type":  {"album"},
		"q":     {fmt.Sprintf("album:%s artist:%s", albumName, artistName)},
		"limit": {"3"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("spotify: build album search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("spotify: album search request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		return "", err
	}
	var asr albumSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&asr); err != nil {
		return "", fmt.Errorf("spotify: decode album search response: %w", err)
	}
	if len(asr.Albums.Items) == 0 {
		return "", nil
	}
	return asr.Albums.Items[0].ID, nil
}

func (c *Client) fetchAlbumTracks(ctx context.Context, albumID, fallbackArtist string) ([]Track, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("spotify: obtain token: %w", err)
	}
	params := url.Values{"limit": {"50"}}
	endpoint := fmt.Sprintf("%s/albums/%s/tracks?%s", c.baseURL, albumID, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: build album tracks request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify: album tracks request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		return nil, err
	}
	var atr albumTracksResponse
	if err := json.NewDecoder(resp.Body).Decode(&atr); err != nil {
		return nil, fmt.Errorf("spotify: decode album tracks response: %w", err)
	}

	tracks := make([]Track, 0, len(atr.Items))
	for _, item := range atr.Items {
		artists := make([]string, 0, len(item.Artists))
		for _, a := range item.Artists {
			artists = append(artists, a.Name)
		}
		if len(artists) == 0 {
			artists = []string{fallbackArtist}
		}
		tracks = append(tracks, Track{
			ID:         item.ID,
			Name:       item.Name,
			Artists:    artists,
			DurationMs: item.DurationMs,
		})
	}
	return tracks, nil
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

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("spotify search",
			"query", query,
			"dur", time.Since(start).String(),
			"err", err.Error(),
		)
		return nil, fmt.Errorf("spotify: search request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := httpx.CheckResponse(serviceName, resp); err != nil {
		c.logger.Error("spotify search",
			"query", query,
			"status", resp.StatusCode,
			"dur", time.Since(start).String(),
			"err", err.Error(),
		)
		return nil, err
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("spotify: decode search response: %w", err)
	}

	c.logger.Info("spotify search",
		"query", query,
		"status", resp.StatusCode,
		"results", len(sr.Tracks.Items),
		"dur", time.Since(start).String(),
	)

	tracks := make([]Track, 0, len(sr.Tracks.Items))
	for _, it := range sr.Tracks.Items {
		artists := make([]string, 0, len(it.Artists))
		for _, a := range it.Artists {
			artists = append(artists, a.Name)
		}
		tracks = append(tracks, Track{
			ID:          it.ID,
			Name:        it.Name,
			Artists:     artists,
			ISRC:        it.ExternalIDs.ISRC,
			AlbumArtURL: largestImageURL(it.Album.Images),
			DurationMs:  it.DurationMs,
		})
	}
	return tracks, nil
}
