# FC-003 Concurrency and durability

- Status: done
- Blocked by: FC-002
- Spec: acceptance scenarios 3, 4, 5, 13

## Outcome

Concurrent and retried writes are safe, durable and eventually distributed when every hint is lost.

## Work

- Separate ConfigRevision/EntityRevision/LeaseRevision types and persistence.
- Add record+collection optimistic checks, active conflict, action_request persistence and canonical request digests.
- Atomically write version/digest/change-log/audit/outbox; implement SKIP LOCKED relay, hint, Watch and version poll.
- Add SDK watch/retry/callback isolation and atomic update convergence.

## Acceptance

- Same Environment/key concurrent base create has one winner; different Environments proceed.
- Same action ID is replay-safe; a new stale action aborts.
- Dropped hint/Watch converges by poll; multi-worker Outbox never duplicates configuration effect.

## Evidence

- Real MySQL 8.4.11 and 8.0.46 concurrency tests prove one active winner for the same Environment/key, independent progress across Environments, concurrent create replay, action replay and stale-authority rejection.
- Action results and canonical request digests persist in `release_action_requests`; duplicate requests produce no second configuration, version, audit or Outbox effect.
- Outbox contract tests cover multi-worker `SKIP LOCKED` claims, LeaseRevision CAS, lease-expiry recovery, retry/dead-letter, and audited DEAD_LETTER replay without changing payload or idempotency key.
- RefreshHint has bounded queueing and EventID TTL deduplication; WatchHub isolates slow consumers with `RESYNC_REQUIRED`; Config Server and SDK version polls converge in a real MySQL tracer while Hint, Watch and Outbox delivery are deliberately absent.
- Full verification passed with `go test ./...`, `go test -race ./...`, `go vet ./...` and `go build ./...`.
