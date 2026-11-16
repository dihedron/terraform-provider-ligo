# A run that waits for a person

A person, and every change to an instance, waits for an approver. The plan shows nothing it cannot know; the apply waits.

## The apply ends at the time-out

When `approval_timeout` passes with the request still pending, the apply fails with a message like:

```text
change request <id> of <kind> <name> is still PENDING after 10m0s; it is awaiting
<side> (<jurisdiction>). It stays in ligo and may still be approved: ask the people
who decide for those jurisdictions, then run the apply again, which waits for it and
does not file another
```

The state does not record the change. The request stays open in `ligo`.

## Finding and deciding it

```bash
ligo cr list --status PENDING --kind binding   # or --name NAME; --awaiting-me for an approver
ligo cr show <id>
```

The approvers of the sides named in the message decide it (`ligo cr approve <id>`). A person cannot decide their own request (`four-eyes-violation`). When a request is not wanted, the one who filed it withdraws it, or an approver declines it.

## Running again

![A run that ends while the request is open does not file twice.](pending-recovery.pdf){width=100%}

The next plan warns `A change request is pending`. When the open request asks for this very change, the apply waits for it and files nothing; if it was already approved, the apply finds the change applied and files nothing. When the open request asks for another change, the apply fails until a person decides it: that request is somebody else's change.

## A crashed run

If the provider's process dies between the filing and the end of the wait, the request is open in `ligo` and the state is unchanged. Run the apply again: it finds the request by its content and waits for it.

## Declined, withdrawn, expired

The apply fails with the request's final status and the decision comments. Nothing is retried; correct the cause and apply again.
