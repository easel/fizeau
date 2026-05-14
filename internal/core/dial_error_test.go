package core

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// dialRefusedError constructs a *net.OpError that matches IsDialError.
func dialRefusedError() error {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("connection refused"),
	}
}

// TestProviderDialError_FailsFastBelow5Seconds asserts that a dial-tcp-refused
// error returns from the provider call in under 5 seconds (no 4-attempt retry
// curve). Corresponds to AC-3.
func TestProviderDialError_FailsFastBelow5Seconds(t *testing.T) {
	p := &retryProvider{
		outcomes: []providerOutcome{
			{err: dialRefusedError()},
		},
	}
	start := time.Now()
	_, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: p,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from dial failure, got nil")
	}
	// The dial error is a fast-fail: no retry backoff delays (which would be
	// 1s + 2s + 4s + 8s = 15s for 4 attempts). Even 5s is generous.
	if elapsed > 5*time.Second {
		t.Errorf("dial error should fail fast: took %v, want < 5s", elapsed)
	}
	// Exactly one attempt: no retries.
	if p.callCount != 1 {
		t.Errorf("want 1 provider call for dial error (no retries), got %d", p.callCount)
	}
}

// TestProviderInflightError_StillRetries asserts that in-flight 5xx errors
// keep the existing retry behavior. This is a regression guard against
// dial-error fast-fail logic accidentally catching in-flight server errors.
// Corresponds to AC-4.
func TestProviderInflightError_StillRetries(t *testing.T) {
	// 503 Service Unavailable is an in-flight transient error — must be retried.
	p := &retryProvider{
		outcomes: []providerOutcome{
			{err: errors.New("503 Service Unavailable")},
			{response: Response{Content: "done"}},
		},
	}
	_, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: p,
	})
	if err != nil {
		t.Fatalf("expected success after 503 retry, got: %v", err)
	}
	// Provider was called twice: 503 was retried and then succeeded.
	if p.callCount != 2 {
		t.Errorf("want 2 provider calls (1 transient failure + 1 success), got %d", p.callCount)
	}
}
