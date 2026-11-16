package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The write path, shared by every resource (spec §3.3): a mutation is a change
// request that is filed once and waited for. The only retry is of a filing that
// ligo refused as rate limited (429, nothing was processed), a few times; and
// nothing here records a change that did not happen.

// filing is one write: what it changes, the payload that identifies it among
// the open requests of the resource, and the call that files it.
type filing struct {
	kind        string // SOURCE, TARGET or BINDING, as the change requests name it
	name        string
	payload     map[string]any // the request body, to recognise a request of ours that is still open
	file        func(context.Context) (adminclient.ChangeRequest, error)
	untilActive bool // a binding: wait until it is ACTIVE
}

// options are the settings of every filing that the session carries.
func (s *Session) options(managed *bool) adminclient.Options {
	return adminclient.Options{ApprovalReference: s.ApprovalReference, AutomationContext: s.AutomationContext, Managed: managed}
}

// apply files f, or waits for the open request that is the same, and returns
// the request once it is APPLIED. Every other end is an error that says what
// happened and what a person can do; the request stays in ligo.
func (s *Session) apply(ctx context.Context, f filing) (adminclient.ChangeRequest, error) {
	cr, found, err := s.openRequest(ctx, f)
	if err != nil {
		return adminclient.ChangeRequest{}, err
	}
	if !found {
		if cr, err = fileRetrying(ctx, f.file); err != nil {
			return adminclient.ChangeRequest{}, writeError(f, err)
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, s.ApprovalTimeout)
	defer cancel()
	last, err := s.Client.WaitChangeRequest(waitCtx, cr.Id.String(), adminclient.WaitOptions{UntilBindingActive: f.untilActive})
	if last.Status == "" {
		last = cr
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return last, fmt.Errorf("interrupted while waiting for change request %s of %s %s (it stays in ligo): %w", cr.Id, strings.ToLower(f.kind), f.name, ctx.Err())
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		return last, fmt.Errorf("change request %s of %s %s is still %s after %s; it is awaiting %s. It stays in ligo and may still be approved: "+
			"ask the people who decide for those jurisdictions, then run the apply again, which waits for it and does not file another",
			cr.Id, strings.ToLower(f.kind), f.name, last.Status, s.ApprovalTimeout, awaiting(last))
	case err != nil:
		return last, fmt.Errorf("waiting for change request %s of %s %s: %w", cr.Id, strings.ToLower(f.kind), f.name, err)
	case !adminclient.IsApplied(last):
		return last, fmt.Errorf("change request %s of %s %s ended %s%s", cr.Id, strings.ToLower(f.kind), f.name, last.Status, decided(last))
	}
	return last, nil
}

// A 429 is refused before ligo processes anything, so filing again cannot file
// twice. Nothing else is retried: a 503 or a reset may have been processed, and
// the next apply finds the open request instead (T-P6).
const (
	filingAttempts = 3
	maxRetryWait   = 30 * time.Second
)

// refusedBackoff is the wait before a retry when ligo gave no Retry-After; a
// variable for the tests.
var refusedBackoff = time.Second

// fileRetrying calls file, again after a 429 for at most filingAttempts calls,
// waiting for what ligo asked (or a growing, jittered pause) and giving up
// when it asks for more than maxRetryWait or the context ends.
func fileRetrying(ctx context.Context, file func(context.Context) (adminclient.ChangeRequest, error)) (adminclient.ChangeRequest, error) {
	for attempt := 1; ; attempt++ {
		cr, err := file(ctx)
		var p *adminclient.Problem
		if err == nil || !errors.As(err, &p) || p.Status != http.StatusTooManyRequests {
			return cr, err
		}
		wait := p.RetryAfter
		if wait == 0 {
			wait = refusedBackoff << (attempt - 1)
			wait += rand.N(wait/4 + 1) //nolint:gosec // jitter, not a secret
		}
		if attempt == filingAttempts || wait > maxRetryWait {
			return cr, fmt.Errorf("rate limited by ligo after %d attempt(s): %w", attempt, err)
		}
		slog.WarnContext(ctx, "ligo refused the filing as rate limited; trying again", "attempt", attempt, "wait", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return cr, fmt.Errorf("interrupted while rate limited by ligo: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// awaiting names the sides that have not decided, with their jurisdictions.
func awaiting(cr adminclient.ChangeRequest) string {
	var out []string
	for _, side := range cr.Sides {
		if side.Decision == nil {
			out = append(out, side.Side+" ("+side.Jurisdiction+")")
		}
	}
	if len(out) == 0 {
		out = cr.AwaitingApproval
	}
	if len(out) == 0 {
		return "a decision"
	}
	return strings.Join(out, ", ")
}

// decided says who decided what, for a request that ended without being applied.
func decided(cr adminclient.ChangeRequest) string {
	var out []string
	for _, side := range cr.Sides {
		if side.Decision != nil && side.DecidedBy != nil {
			out = append(out, fmt.Sprintf("%s: %s by %s", side.Side, *side.Decision, *side.DecidedBy))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return " (" + strings.Join(out, "; ") + ")"
}

// pending finds the request that is still open for the resource. found says
// there is one; same says that it asks for what f asks for (T-P6).
func (s *Session) pending(ctx context.Context, f filing) (cr adminclient.ChangeRequest, found, same bool, err error) {
	for open, err := range s.Client.ListChangeRequests(ctx, adminclient.ChangeRequestFilter{Status: "PENDING", ResourceType: f.kind, ResourceName: f.name}) {
		if err != nil {
			return cr, false, false, fmt.Errorf("looking for an open change request of %s %s: %w", strings.ToLower(f.kind), f.name, err)
		}
		full, err := s.Client.GetChangeRequest(ctx, open.Id.String())
		if err != nil {
			return cr, false, false, fmt.Errorf("reading change request %s: %w", open.Id, err)
		}
		return full, true, full.Payload != nil && covers(*full.Payload, f.payload), nil
	}
	return cr, false, false, nil
}

// openRequest is the pending request that apply waits for instead of filing
// one; another content is somebody else's change, which a person resolves.
func (s *Session) openRequest(ctx context.Context, f filing) (adminclient.ChangeRequest, bool, error) {
	cr, found, same, err := s.pending(ctx, f)
	switch {
	case err != nil:
		return cr, false, err
	case found && !same:
		return cr, false, fmt.Errorf("change request %s of %s %s is pending with another content, awaiting %s: a person approves or declines it first, "+
			"or the one who filed it withdraws it; then run the apply again", cr.Id, strings.ToLower(f.kind), f.name, awaiting(cr))
	}
	return cr, found, nil
}

// covers reports whether every key of want is in have with the same value, in
// normal form: the server adds what it derives (the jurisdiction, defaults).
func covers(have, want map[string]any) bool {
	for k, w := range want {
		h, ok := have[k]
		if !ok {
			return false
		}
		if hm, ok := h.(map[string]any); ok {
			if wm, ok := w.(map[string]any); ok {
				if !covers(hm, wm) {
					return false
				}
				continue
			}
		}
		hs, err1 := jsonnorm.Of(h)
		ws, err2 := jsonnorm.Of(w)
		if err1 != nil || err2 != nil || hs != ws {
			return false
		}
	}
	return true
}

// writeError explains a refused filing. The admin client's errors carry the
// problem's type and detail, never a body (T-P2); none is retried (T-P5).
func writeError(f filing, err error) error {
	what := strings.ToLower(f.kind) + " " + f.name
	switch {
	case errors.Is(err, adminclient.ErrConflict):
		return fmt.Errorf("%s changed outside Terraform (its revision is not the one in the state): run terraform refresh or plan, and apply again; nothing was retried: %w", what, err)
	case errors.Is(err, adminclient.ErrState) && problemSlug(err) == "resource-held":
		return fmt.Errorf("%s is held: a person removed it in an emergency and a person lifts the hold (ligo hold list, ligo hold lift) after reviewing it. "+
			"The provider never lifts a hold and does not file again until then: %w", what, err)
	case errors.Is(err, adminclient.ErrState) && problemSlug(err) == "break-glass-open":
		return fmt.Errorf("%s has an open break-glass change, which a person must have reviewed before ligo takes another change; the request is named below. "+
			"The provider does not file over it: %w", what, err)
	case errors.Is(err, adminclient.ErrState):
		return fmt.Errorf("%s: ligo refuses the change in its present state (a lock, a hold, bindings that use it or a request that is pending): %w", what, err)
	case errors.Is(err, adminclient.ErrForbidden):
		return fmt.Errorf("%s: the caller may not file this change: %w", what, err)
	default:
		return fmt.Errorf("filing the change of %s: %w", what, err)
	}
}

func problemSlug(err error) string {
	var p *adminclient.Problem
	if errors.As(err, &p) {
		return p.Slug()
	}
	return ""
}

// stateChange files a change of state (deprecate, reactivate, retire, of a
// resource or of one of its access levels) with the revision it is based on.
// It carries no lock: ligo refuses a managed flag on it.
func (s *Session) stateChange(ctx context.Context, kind, name string, call func(context.Context) (adminclient.ChangeRequest, error)) (adminclient.ChangeRequest, error) {
	return s.apply(ctx, filing{kind: kind, name: name, file: call})
}

// lockChecks is what a plan refuses or says before any filing (spec §3.7): a
// person cannot own, another automation's resource is not overwritten, and a
// request that is still pending is told. changes is false for a plan that has
// nothing to file.
func (s *Session) lockChecks(ctx context.Context, kind, name string, managed bool, stateOwner types.String, exists, changes bool, payload map[string]any, diags *diag.Diagnostics) {
	if !changes {
		return
	}
	if managed && s.Person {
		diags.AddAttributeError(path.Root("managed"), "A person cannot own a resource",
			"The provider authenticates with username, so it is a person, and ligo refuses managed resources from a person. "+
				"Set managed = false on the resource (or managed = false in the provider), or authenticate with a SPIFFE identity.")
	}
	if owner := stateOwner.ValueString(); exists && owner != "" && s.CallerID != "" && owner != s.CallerID {
		diags.AddError("Managed by another automation",
			fmt.Sprintf("The %s %s is managed by %s, not by this caller (%s). Two pipelines never change one resource: change it from its owner, "+
				"or have a person release it.", strings.ToLower(kind), name, owner, s.CallerID))
	}
	if diags.HasError() {
		return
	}
	if cr, found, same, err := s.pending(ctx, filing{kind: kind, name: name, payload: payload}); err == nil && found {
		msg := fmt.Sprintf("Change request %s is pending for the %s, awaiting %s.", cr.Id, strings.ToLower(kind), awaiting(cr))
		if same {
			msg += " It asks for this very change: the apply waits for it and does not file another."
		} else {
			msg += " It asks for another change: the apply fails until a person decides it."
		}
		diags.AddWarning("A change request is pending", msg)
	}
}
