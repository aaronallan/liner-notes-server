package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronpollock/liner-notes-server/internal/httpx"
)

func fastConfig() Config {
	return Config{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func TestDo_SuccessFirstTry(t *testing.T) {
	calls := 0
	got, err := Do(context.Background(), fastConfig(), func(context.Context) (int, error) {
		calls++
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	got, err := Do(context.Background(), fastConfig(), func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "", &httpx.APIError{Service: "svc", StatusCode: 503}
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want ok", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_DoesNotRetryNonRetryable(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), fastConfig(), func(context.Context) (int, error) {
		calls++
		return 0, &httpx.APIError{Service: "svc", StatusCode: 404}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (404 is not retryable)", calls)
	}
}

func TestDo_ExhaustsAttempts(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), fastConfig(), func(context.Context) (int, error) {
		calls++
		return 0, &httpx.APIError{Service: "svc", StatusCode: 500}
	})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (MaxAttempts)", calls)
	}
}

func TestDo_RespectsContextCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{MaxAttempts: 5, BaseDelay: time.Hour, MaxDelay: time.Hour}

	calls := 0
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := Do(ctx, cfg, func(context.Context) (int, error) {
		calls++
		return 0, &httpx.APIError{Service: "svc", StatusCode: 500}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDo_RateLimitRetried(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), fastConfig(), func(context.Context) (int, error) {
		calls++
		if calls < 2 {
			return 0, &httpx.RateLimitError{Service: "svc", RetryAfter: time.Millisecond}
		}
		return 1, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}
