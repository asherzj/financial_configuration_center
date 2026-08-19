# FC-009 Metadata, diagnostics and dead-letter operations

- Status: in-progress
- Blocked by: FC-008
- Spec: actor 4.1 and 4.5, acceptance scenario 13

## Outcome

Administrators can safely manage metadata and operators can diagnose snapshots/audit/Outbox without accessing configuration bodies.

## Work

- Complete Collection/Subscription/Model/Template CRUD/versioning and compile preview UI.
- Enforce SDKDeliveryEnabled and release-type applicability reason codes.
- Add audit filters, snapshot/collection status and bounded diagnostics.
- Add DEAD_LETTER replay with LeaseRevision/reason/confirmation and permissions.

## Acceptance

- Revision conflict and model path errors are stable and displayed without losing edits.
- Diagnostics contain no record data; concurrent replay has one CAS winner and append-only audit.

## Evidence

- Completed bounded, payload-free Outbox metadata listing and DEAD_LETTER replay across MySQL, application authorization, Kitex gRPC, Admin BFF, OpenAPI, and the `/diagnostics` console page.
- Replay requires `PLATFORM_OPERATOR`, the current LeaseRevision, a reason, and the exact event-specific confirmation phrase. MySQL 8.0/8.4 verify stale CAS rejection, one replay audit row, and payload/idempotency preservation.
- `go test ./... -count=1`
- `go test -race ./internal/adminbff ./internal/outbox/... -count=1`
- `pnpm --dir web/admin-console test`
- `pnpm --dir web/admin-console typecheck`
- `pnpm --dir web/admin-console lint`
- `pnpm --dir web/admin-console build`
- Remaining: Catalog/Template management, audit filters, and snapshot/collection diagnostics.
