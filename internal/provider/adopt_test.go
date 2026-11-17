package provider

import (
	"strings"
	"testing"
	"time"
)

// A create whose answer was lost (a replica killed, a runner cut off) leaves
// the resource in ligo and not in the state; the next apply must not need a
// person to import it. Found by the pre-release campaign: a replica killed
// while 60 sources were filed left some of them applied and unknown to
// Terraform, and every rerun failed with 409 "is in use".

// What this caller already filed and ligo applied, with the same content, is
// adopted: nothing is filed again.
func TestCreateAdoptsWhatThisCallerAlreadyApplied(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = true
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	first, resp := create(t, r, sch, planned("s", "^x$"))
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	// the state was lost: the same plan, again
	again, resp := create(t, r, sch, planned("s", "^x$"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("the second create: %s", errorText(resp.Diagnostics))
	}
	if l.filings != 1 {
		t.Errorf("%d filings, want 1: the applied one is adopted", l.filings)
	}
	if again.Revision != first.Revision || again.ManagedBy != first.ManagedBy {
		t.Errorf("adopted %+v, first %+v", again, first)
	}
}

// What nobody of ours filed is somebody else's, or an older run's: the error
// says what to do instead of guessing.
func TestCreateDoesNotAdoptWhatItDidNotFile(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = true
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	l.seedSource("s") // exists, with no change request of this caller
	_, resp := create(t, r, sch, planned("s", "^x$"))
	if !resp.Diagnostics.HasError() {
		t.Fatal("a create over a resource it did not file succeeded")
	}
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "terraform import") {
		t.Errorf("the error does not say to import: %q", msg)
	}
}

// Another content is not adopted either.
func TestCreateDoesNotAdoptAnotherContent(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = true
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	if _, resp := create(t, r, sch, planned("s", "^x$")); resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	_, resp := create(t, r, sch, planned("s", "^y$"))
	if !resp.Diagnostics.HasError() {
		t.Fatal("a create with another content was adopted")
	}
}
