# FC-003 Concurrency and durability

- Status: in-progress
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

Not run.
