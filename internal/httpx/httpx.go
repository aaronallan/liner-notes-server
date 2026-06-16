// Package httpx holds shared HTTP helpers for the upstream API clients:
// structured error types and 429 rate-limit handling. Centralizing these keeps
// every client's error semantics identical and lets the retry layer reason about
// failures uniformly.
package httpx

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// maxErrorBody bounds how much of an error response body we read for diagnostics.
const maxErrorBody = 4 << 10

// APIError represents a non-2xx response that is not a rate limit.
type APIError struct {
	Service    string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: unexpected status %d: %s", e.Service, e.StatusCode, e.Body)
}

// RateLimitError represents an HTTP 429 response. RetryAfter carries the parsed
// Retry-After hint (zero if the upstream did not provide one).
type RateLimitError struct {
	Service    string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s: rate limited (retry after %s)", e.Service, e.RetryAfter)
}

// retryable is implemented by errors that represent transient failures worth
// retrying.
type retryable interface {
	Retryable() bool
}

// Retryable reports whether the rate limit is transient. It always is.
func (e *RateLimitError) Retryable() bool { return true }

// Retryable reports whether the status code indicates a transient server error.
func (e *APIError) Retryable() bool { return e.StatusCode >= 500 }

// IsRetryable reports whether err represents a transient failure (a rate limit
// or a 5xx). The retry layer uses this to decide whether to back off and retry.
func IsRetryable(err error) bool {
	r, ok := err.(retryable)
	return ok && r.Retryable()
}

// CheckResponse inspects resp and returns nil for 2xx, a *RateLimitError for 429
// (with any Retry-After parsed), or an *APIError otherwise. It does not close the
// response body.
func CheckResponse(service string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{
			Service:    service,
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &APIError{Service: service, StatusCode: resp.StatusCode, Body: string(body)}
}

// ParseRetryAfter interprets a Retry-After header value, which may be either a
// number of seconds or an HTTP-date. It returns the delay relative to now,
// clamped to a non-negative duration; unparseable or absent values yield 0.
func ParseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
