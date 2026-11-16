package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
)

// The write path, shared by every resource (spec §3.3): a mutation is a change
// request that is filed once and waited for. Nothing here retries a write, and
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
		if cr, err = f.file(ctx); err != nil {
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
	case errors.Is(err, adminclient.ErrState):
		return fmt.Errorf("%s: ligo refuses the change in its present state (a lock, a hold, bindings that use it or a request that is pending): %w", what, err)
	case errors.Is(err, adminclient.ErrForbidden):
		return fmt.Errorf("%s: the caller may not file this change: %w", what, err)
	default:
		return fmt.Errorf("filing the change of %s: %w", what, err)
	}
}
