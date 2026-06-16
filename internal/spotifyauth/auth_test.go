package spotifyauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer returns an httptest server that mimics the Spotify accounts token
// endpoint. It records how many times it was hit so tests can assert caching and
// single-flight behaviour, and serves a token with the given lifetime.
func tokenServer(t *testing.T, accessToken string, expiresIn int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)

		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected Content-Type %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("expected grant_type=client_credentials, got %q", got)
		}
		// Credentials must travel via HTTP Basic auth, not the body.
		if _, _, ok := r.BasicAuth(); !ok {
			t.Errorf("expected basic auth header")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestClientCredentials_FetchesToken(t *testing.T) {
	srv, hits := tokenServer(t, "tok-123", 3600)

	cc := NewClientCredentials("id", "secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))

	got, err := cc.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if got != "tok-123" {
		t.Errorf("got token %q, want %q", got, "tok-123")
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
}

func TestClientCredentials_SendsBasicAuthCredentials(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600})
	}))
	t.Cleanup(srv.Close)

	cc := NewClientCredentials("my-id", "my-secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatalf("Token() error: %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("my-id:my-secret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestClientCredentials_CachesToken(t *testing.T) {
	srv, hits := tokenServer(t, "cached", 3600)
	cc := NewClientCredentials("id", "secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))

	for i := 0; i < 5; i++ {
		if _, err := cc.Token(context.Background()); err != nil {
			t.Fatalf("Token() error: %v", err)
		}
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1 (token should be cached)", *hits)
	}
}

func TestClientCredentials_RefreshesAfterExpiry(t *testing.T) {
	srv, hits := tokenServer(t, "tok", 3600)

	now := time.Now()
	clock := func() time.Time { return now }
	cc := NewClientCredentials("id", "secret",
		WithTokenURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(func() time.Time { return clock() }),
		WithEarlyRefresh(60*time.Second),
	)

	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatalf("first Token() error: %v", err)
	}
	if *hits != 1 {
		t.Fatalf("after first call hits=%d, want 1", *hits)
	}

	// Advance time past the token lifetime; a refresh must occur.
	now = now.Add(2 * time.Hour)
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatalf("second Token() error: %v", err)
	}
	if *hits != 2 {
		t.Errorf("after expiry hits=%d, want 2 (token should refresh)", *hits)
	}
}

func TestClientCredentials_RefreshesEarly(t *testing.T) {
	// Token lives 100s but early-refresh window is 60s, so once 50s have passed
	// (within the window) the next call must refresh.
	srv, hits := tokenServer(t, "tok", 100)
	now := time.Now()
	cc := NewClientCredentials("id", "secret",
		WithTokenURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(func() time.Time { return now }),
		WithEarlyRefresh(60*time.Second),
	)

	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	now = now.Add(50 * time.Second) // 50s left < 60s early-refresh window
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if *hits != 2 {
		t.Errorf("hits=%d, want 2 (should refresh inside early window)", *hits)
	}
}

func TestClientCredentials_SingleFlightUnderConcurrency(t *testing.T) {
	srv, hits := tokenServer(t, "tok", 3600)
	cc := NewClientCredentials("id", "secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := cc.Token(context.Background()); err != nil {
				t.Errorf("Token() error: %v", err)
			}
		}()
	}
	wg.Wait()

	if *hits != 1 {
		t.Errorf("server hit %d times under concurrency, want 1 (no stampede)", *hits)
	}
}

func TestClientCredentials_ErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_client", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	cc := NewClientCredentials("id", "secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := cc.Token(context.Background()); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestClientCredentials_ErrorOnMissingCredentials(t *testing.T) {
	cc := NewClientCredentials("", "", WithTokenURL("http://example.invalid"))
	_, err := cc.Token(context.Background())
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error %q should mention credentials", err)
	}
}

func TestClientCredentials_RespectsContextCancellation(t *testing.T) {
	srv, _ := tokenServer(t, "tok", 3600)
	cc := NewClientCredentials("id", "secret", WithTokenURL(srv.URL), WithHTTPClient(srv.Client()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cc.Token(ctx); err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}
