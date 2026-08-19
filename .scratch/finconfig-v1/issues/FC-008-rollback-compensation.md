# FC-008 Rollback and compensating release

- Status: in-progress
- Blocked by: FC-007
- Spec: acceptance scenario 12

## Outcome

Every mutable in-progress effect is exactly reversible and successful history is reversed only through a new reviewed order.

## Work

- Complete versioned Overlay/Base/Percent effect codecs and reject unknown versions.
- Inverse all executed effects in reverse order in one transaction.
- Generate reverse items for a current authorized compensation template and revalidate authority/revisions.
- Add rollback/compensation UI and diff lineage.

## Acceptance

- Base records and removed overlays restore byte-equivalent canonical facts after rollback.
- SUCCEEDED cannot roll back; compensation creates a linked order and refuses third-party drift.

## Evidence

- Completed strict versioned effect envelopes and whole-chain in-progress rollback.
- Completed BASE_FINAL ADD/MODIFY/DELETE execution with exact restoration of base before-images and removed scoped overlays.
- Verified the BASE_FINAL modify/delete/rollback transaction on MySQL 8.0 and 8.4; collection revisions advance once per apply/rollback and each mutation emits one outbox event.
- Completed `CreateCompensatingRelease` across domain, application, MySQL, Kitex gRPC, Admin BFF, and OpenAPI. A SUCCEEDED source remains immutable; the new order uses the active `compensation` template and persists `compensates_order_id`.
- Verified linked, manually reviewed compensation, idempotent replay, successful-order rollback refusal, and target-drift refusal on MySQL 8.0 and 8.4.
- `go test ./... -count=1`
- `go test -race ./internal/release/... ./internal/adminbff -count=1`
- Remaining: compensation UI and visible diff lineage.
