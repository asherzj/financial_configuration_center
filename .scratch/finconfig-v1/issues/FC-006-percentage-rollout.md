# FC-006 Percentage rollout

- Status: blocked
- Blocked by: FC-005
- Spec: acceptance scenario 8

## Outcome

Deterministic staged client rollout can be compared and promoted to an Environment base without residual overlays.

## Work

- TDD SHA-256/NUL bucket vectors, 0..99 range validation and monotonic union.
- Implement PERCENT_ROLLOUT effects, SDK bucket filtering, PreviewBucket and COMPARE results.
- Implement BASE_FINAL promotion, removed-rule BaseStepEffect and exact rollback.
- Add percentage progress/preview/compare UI.

## Acceptance

- Fixed ConsumerID/ClientID vectors are stable; two clients can see different rollout values.
- Promotion changes only target Environment, removes process rules and rollback restores base+ranges.

## Evidence

Not run.
