// Package retry provides bounded exponential-backoff retries for transient
// upstream failures. It is upstream-agnostic: it retries any error that package
// httpx classifies as retryable (5xx, timeouts surfaced as such, and 429), and
// honours a rate limit's Retry-After hint when present.
package retry

import (
	"context"
	"errors"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

// Config tunes the backoff schedule.
type Config struct {
	// MaxAttempts is the total number of calls, including the first. Values < 1
	// are treated as 1 (no retries).
	MaxAttempts int
	// BaseDelay is the backoff before the first retry; it doubles each attempt.
	BaseDelay time.Duration
	// MaxDelay caps the per-attempt backoff.
	MaxDelay time.Duration
}

// DefaultConfig is a sensible schedule for the best-effort Spotify/ReccoBeats
// calls, which have no SLA.
func DefaultConfig() Config {
	return Config{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second}
}

// Do calls fn until it succeeds, returns a non-retryable error, or exhausts the
// attempt budget. Between attempts it sleeps with exponential backoff, returning
// early (with the context's error) if ctx is cancelled while waiting.
func Do[T any](ctx context.Context, cfg Config, fn func(context.Context) (T, error)) (T, error) {
	attempts := cfg.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var zero T
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		lastErr = err

		// Give up immediately on errors that won't improve with a retry.
		if !httpx.IsRetryable(err) {
			return zero, err
		}
		// No point sleeping after the final attempt.
		if attempt == attempts-1 {
			break
		}

		if err := sleep(ctx, backoff(cfg, attempt, err)); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
}

// backoff computes the delay before the next attempt: exponential growth capped
// at MaxDelay, overridden by a rate limit's Retry-After when that is longer.
func backoff(cfg Config, attempt int, err error) time.Duration {
	delay := cfg.BaseDelay << attempt
	if delay > cfg.MaxDelay || delay <= 0 {
		delay = cfg.MaxDelay
	}
	var rl *httpx.RateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > delay {
		delay = rl.RetryAfter
	}
	return delay
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
