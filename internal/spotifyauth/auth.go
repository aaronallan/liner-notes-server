// Package spotifyauth implements the Spotify OAuth2 Client Credentials flow,
// providing cached bearer tokens for the Spotify Web API clients.
package spotifyauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultTokenURL is the Spotify accounts service token endpoint for the
// Client Credentials flow.
const DefaultTokenURL = "https://accounts.spotify.com/api/token"

// defaultEarlyRefresh is how long before a token's stated expiry it is treated as
// expired, so a fresh token is fetched before in-flight requests can fail.
const defaultEarlyRefresh = 60 * time.Second

// ErrMissingCredentials is returned when the client ID or secret is empty.
var ErrMissingCredentials = errors.New("spotifyauth: missing client credentials")

// ClientCredentials implements the Spotify OAuth2 Client Credentials flow.
//
// There is no end user and no refresh token: a new bearer token is requested
// from the token endpoint and reused until shortly before it expires. Access is
// serialized by a mutex so concurrent callers never stampede the token endpoint
// with parallel refreshes — the first caller fetches, the rest reuse the result.
type ClientCredentials struct {
	clientID     string
	clientSecret string
	tokenURL     string
	httpClient   *http.Client
	now          func() time.Time
	earlyRefresh time.Duration

	mu     sync.Mutex
	cached cachedToken
}

type cachedToken struct {
	accessToken string
	expiresAt   time.Time
}

// Option configures a ClientCredentials.
type Option func(*ClientCredentials)

// WithTokenURL overrides the token endpoint (useful for tests).
func WithTokenURL(u string) Option {
	return func(c *ClientCredentials) { c.tokenURL = u }
}

// WithHTTPClient sets the HTTP client used to reach the token endpoint.
func WithHTTPClient(h *http.Client) Option {
	return func(c *ClientCredentials) { c.httpClient = h }
}

// WithClock overrides the time source (useful for tests).
func WithClock(now func() time.Time) Option {
	return func(c *ClientCredentials) { c.now = now }
}

// WithEarlyRefresh sets how long before stated expiry a token is refreshed.
func WithEarlyRefresh(d time.Duration) Option {
	return func(c *ClientCredentials) { c.earlyRefresh = d }
}

// NewClientCredentials builds a token provider for the Client Credentials flow.
// The client ID and secret must be sourced from the environment or a secret
// manager — never hardcoded.
func NewClientCredentials(clientID, clientSecret string, opts ...Option) *ClientCredentials {
	c := &ClientCredentials{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     DefaultTokenURL,
		httpClient:   http.DefaultClient,
		now:          time.Now,
		earlyRefresh: defaultEarlyRefresh,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Token returns a valid bearer access token, fetching a new one only when the
// cache is empty or within the early-refresh window of expiry.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	if c.clientID == "" || c.clientSecret == "" {
		return "", ErrMissingCredentials
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached.accessToken != "" && c.now().Before(c.cached.expiresAt) {
		return c.cached.accessToken, nil
	}

	tok, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	c.cached = tok
	return tok.accessToken, nil
}

// tokenResponse models the Spotify token endpoint JSON payload.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *ClientCredentials) fetchToken(ctx context.Context) (cachedToken, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return cachedToken{}, fmt.Errorf("spotifyauth: build token request: %w", err)
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return cachedToken{}, fmt.Errorf("spotifyauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return cachedToken{}, fmt.Errorf("spotifyauth: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cachedToken{}, fmt.Errorf("spotifyauth: token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return cachedToken{}, fmt.Errorf("spotifyauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return cachedToken{}, errors.New("spotifyauth: token endpoint returned empty access_token")
	}

	lifetime := time.Duration(tr.ExpiresIn) * time.Second
	expiresAt := c.now().Add(lifetime - c.earlyRefresh)
	return cachedToken{accessToken: tr.AccessToken, expiresAt: expiresAt}, nil
}
