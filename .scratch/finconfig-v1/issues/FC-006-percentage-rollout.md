# FC-006 Percentage rollout

- Status: done
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

- Stable bucket protocol and fixed vectors: `a4ec4e6`.
- Template validation and monotonic range compilation: `6455ea3`.
- Domain/MySQL percentage effects: `70cb87f`.
- Config Server and SDK per-client delivery on MySQL 8.0/8.4: `b90c711`.
- BASE_FINAL promotion and exact base/range rollback on MySQL 8.0/8.4: `6cfaf57`.
- COMPARE success/mismatch persistence, failed audit and RPC/BFF contracts on MySQL 8.0/8.4: `f8ee3a8`.
- Admin preview bucket, rollout coverage and compare diagnostics UI: full lint, typecheck, Vitest and production build passed.
