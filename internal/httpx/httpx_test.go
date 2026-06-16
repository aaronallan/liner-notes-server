package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newResponse(t *testing.T, status int, header http.Header) *http.Response {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, vals := range header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body-text"))
	}))
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

func TestCheckResponse_Success(t *testing.T) {
	resp := newResponse(t, http.StatusOK, nil)
	defer resp.Body.Close()
	if err := CheckResponse("svc", resp); err != nil {
		t.Errorf("CheckResponse(200) = %v, want nil", err)
	}
}

func TestCheckResponse_APIError(t *testing.T) {
	resp := newResponse(t, http.StatusInternalServerError, nil)
	defer resp.Body.Close()

	err := CheckResponse("spotify", resp)
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.Service != "spotify" {
		t.Errorf("Service = %q, want spotify", apiErr.Service)
	}
}

func TestCheckResponse_RateLimitWithRetryAfterSeconds(t *testing.T) {
	h := http.Header{"Retry-After": {"7"}}
	resp := newResponse(t, http.StatusTooManyRequests, h)
	defer resp.Body.Close()

	err := CheckResponse("spotify", resp)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *RateLimitError, got %T (%v)", err, err)
	}
	if rl.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", rl.RetryAfter)
	}
}

func TestCheckResponse_RateLimitWithoutRetryAfter(t *testing.T) {
	resp := newResponse(t, http.StatusTooManyRequests, nil)
	defer resp.Body.Close()

	err := CheckResponse("svc", resp)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *RateLimitError, got %T", err)
	}
	if rl.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 when header absent", rl.RetryAfter)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "12", 12 * time.Second},
		{"negative clamped", "-5", 0},
		{"http date", now.Add(30 * time.Second).UTC().Format(http.TimeFormat), 30 * time.Second},
		{"garbage", "soon", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseRetryAfter(tt.header, now); got != tt.want {
				t.Errorf("ParseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestRateLimitError_IsRetryable(t *testing.T) {
	var err error = &RateLimitError{Service: "svc", RetryAfter: time.Second}
	if !IsRetryable(err) {
		t.Error("RateLimitError should be retryable")
	}
}

func TestAPIError_RetryableOn5xx(t *testing.T) {
	if !IsRetryable(&APIError{StatusCode: 503}) {
		t.Error("5xx APIError should be retryable")
	}
	if IsRetryable(&APIError{StatusCode: 404}) {
		t.Error("404 APIError should not be retryable")
	}
}
