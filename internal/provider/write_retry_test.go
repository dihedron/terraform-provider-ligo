package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
)

func fastRetry(t *testing.T) {
	t.Helper()
	old := refusedBackoff
	refusedBackoff = time.Millisecond
	t.Cleanup(func() { refusedBackoff = old })
}

// A 429 is refused before anything is processed: the filing is tried again and
// ends up filed once.
func TestFilingRetriedAfterRateLimit(t *testing.T) {
	fastRetry(t)
	l := newLigoFake(t)
	l.refuse = map[int]int{429: 2}
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	_, resp := create(t, r, sch, planned("s", "^x$"))
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if l.count() != 1 || l.attempts != 3 {
		t.Errorf("%d filings in %d attempts, want 1 in 3", l.count(), l.attempts)
	}
}

func TestFilingRetryIsBounded(t *testing.T) {
	fastRetry(t)
	l := newLigoFake(t)
	l.refuse = map[int]int{429: 100}
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	_, resp := create(t, r, sch, planned("s", "^x$"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "rate limited by ligo after 3 attempt") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if l.attempts != filingAttempts || l.count() != 0 || !resp.State.Raw.IsNull() {
		t.Errorf("%d attempts, %d filings, state %v", l.attempts, l.count(), resp.State.Raw)
	}
}

// Retry-After is honoured, and one that asks for too long is not waited for.
func TestFilingRetryHonoursRetryAfter(t *testing.T) {
	l := newLigoFake(t)
	l.refuse, l.retryAfter = map[int]int{429: 1}, "1"
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	start := time.Now()
	_, resp := create(t, r, sch, planned("s", "^x$"))
	if resp.Diagnostics.HasError() || time.Since(start) < time.Second || l.attempts != 2 {
		t.Errorf("%v after %s, %d attempts", errorText(resp.Diagnostics), time.Since(start), l.attempts)
	}

	l = newLigoFake(t)
	l.refuse, l.retryAfter = map[int]int{429: 1}, "60"
	r, sch = srcRes(t, ligoSession(t, l, 5*time.Second))
	_, resp = create(t, r, sch, planned("s", "^x$"))
	if !resp.Diagnostics.HasError() || l.attempts != 1 {
		t.Errorf("a wait of 60s must not be taken: %d attempts", l.attempts)
	}
}

// Nothing but a 429 is retried: a 503 may have been processed.
func TestFilingOtherRefusalsAreNotRetried(t *testing.T) {
	fastRetry(t)
	for _, status := range []int{503, 502, 500} {
		l := newLigoFake(t)
		l.refuse = map[int]int{status: 100}
		r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
		_, resp := create(t, r, sch, planned("s", "^x$"))
		if !resp.Diagnostics.HasError() || l.attempts != 1 {
			t.Errorf("%d: %d attempts", status, l.attempts)
		}
	}
}

func TestFileRetryingStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	file := func(context.Context) (adminclient.ChangeRequest, error) {
		calls++
		cancel()
		return adminclient.ChangeRequest{}, &adminclient.Problem{Status: 429, RetryAfter: 10 * time.Second}
	}
	_, err := fileRetrying(ctx, file)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("%d calls, %v", calls, err)
	}
}
