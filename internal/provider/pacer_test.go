package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
)

// ligo limits a principal to about ten requests a second; a run of dozens of
// resources, applied ten at a time, goes over it (found by the pre-release
// campaign on a destroy of 60 sources). The session spaces the calls that
// start a write and holds them all back when ligo says to slow down.

func TestPacerSpacesConcurrentCalls(t *testing.T) {
	p := &pacer{gap: 20 * time.Millisecond}
	start := time.Now()
	var wg sync.WaitGroup
	for range 11 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.wait(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := time.Since(start); got < 10*20*time.Millisecond {
		t.Errorf("11 calls took %v, want at least %v apart", got, 10*20*time.Millisecond)
	}
}

func TestPacerHoldsEveryoneBackAfterSlowDown(t *testing.T) {
	p := &pacer{gap: time.Millisecond}
	p.slowDown(80 * time.Millisecond)
	start := time.Now()
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := time.Since(start); got < 70*time.Millisecond {
		t.Errorf("waited %v after a slow-down of 80ms", got)
	}
}

func TestPacerStopsWithTheContext(t *testing.T) {
	p := &pacer{gap: time.Millisecond}
	p.slowDown(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := p.wait(ctx); err == nil {
		t.Error("a wait that the context ended returned nil")
	}
}

// The lookup of an open request is read-only: a 429 that the client's own
// retries did not clear is tried again, paced, instead of failing the apply.
func TestPendingLookupRetriedAfterRateLimit(t *testing.T) {
	fastRetry(t)
	l := newLigoFake(t)
	l.refuseReads = 5 // more than the client retries
	s := ligoSession(t, l, 5*time.Second)
	if _, _, _, err := s.pending(context.Background(), filing{kind: "SOURCE", name: "s"}); err != nil {
		t.Fatalf("a rate-limited lookup failed the apply: %v", err)
	}
}

// A read of a resource (a refresh of 60 sources at ten at a time) waits its turn
// and is tried again after a 429 that the client's own retries did not clear.
func TestPacedReadRetriesAfterRateLimit(t *testing.T) {
	s := &Session{}
	s.pace.gap = time.Millisecond
	calls := 0
	got, err := paced(context.Background(), s, func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "", &adminclient.Problem{Status: 429, RetryAfter: time.Millisecond}
		}
		return "read", nil
	})
	if err != nil || got != "read" || calls != 3 {
		t.Errorf("got %q, %v after %d calls, want the read after 3", got, err, calls)
	}
}

func TestPacedReadIsBounded(t *testing.T) {
	s := &Session{}
	s.pace.gap = time.Millisecond
	calls := 0
	_, err := paced(context.Background(), s, func(context.Context) (string, error) {
		calls++
		return "", &adminclient.Problem{Status: 429, RetryAfter: time.Millisecond}
	})
	if err == nil || calls != lookupAttempts {
		t.Errorf("%d calls and %v, want %d calls and the 429", calls, err, lookupAttempts)
	}
}

func TestPacedReadDoesNotRetryOtherErrors(t *testing.T) {
	s := &Session{}
	calls := 0
	_, err := paced(context.Background(), s, func(context.Context) (string, error) {
		calls++
		return "", &adminclient.Problem{Status: 404}
	})
	if err == nil || calls != 1 {
		t.Errorf("%d calls and %v, want one call", calls, err)
	}
}
