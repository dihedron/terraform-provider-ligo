# The write path

Every mutation is a change request. The provider files it once and waits.

![One write: file once, wait, record only what was applied.](apply-sequence.pdf){width=100%}

## Rules

1. **File once.** The client never retries a write. The only retry is a filing that `ligo` refused as rate limited (429, refused before anything is processed): at most 3 attempts, waiting for `Retry-After`, never longer than 30 seconds, with jitter, and ending when the context ends. The session has one pacer, shared by every resource of the run: it spaces the calls that start a write (about seven a second, under `ligo`'s ten for a principal) and, on a 429, holds all of them back. The lookup of an open request is a read and is tried again, at most 5 times.
2. **Wait for the request.** Applied ends the write; a binding continues until `ACTIVE`. Pending at `approval_timeout` fails with the request ID and the sides still awaited. Rejected, withdrawn, expired or failed fail with the status and the comments.
3. **Record only what was applied.** After a failed wait the state does not hold the change; the request stays in `ligo`.
4. **A stale revision is never retried.** The revision in the state goes as `expectedRevision`; a conflict asks for a refresh.
5. **Recognise a pending request.** Before filing, and in the plan, the provider looks for an open request of the same resource. If every key of its payload is in the open request with the same normalised value (`covers`: the server adds what it derives, such as the jurisdiction and defaults), it is the same change and the provider waits for it. If it differs, the apply fails until a person decides it.

![A run that ends while the request is open does not file twice.](pending-recovery.pdf){width=100%}

6. **Adopt a lost create.** If a filing finds the name taken (`invalid-transition`), the provider looks for the request that created it: filed by this caller (a pipeline, by its SPIFFE ID), applied, with a payload that says the same. If there is one, that is the filing and nothing is filed again; if not, the apply stops and says to import. A person's runs are never adopted: the provider cannot tell who they were.

## Called methods

A test lists the admin client methods the provider calls and fails on any other: the decision methods (approve, decline, ratify, revert), `LiftHold`, release calls and `RetryBinding`/`RotateBinding` are not on the list.

## Removal

Deleting files the retirement (a binding: the revocation, without `managed`) and waits. `ligo` may refuse (bindings exist): the problem is shown.
