# FC-009 Metadata, diagnostics and dead-letter operations

- Status: done
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
- Completed payload-free Snapshot/collection diagnostics across the atomic Snapshot Manager, Kitex DiagnosticsService, Admin BFF, OpenAPI, and console. Refresh failures expose only stable error codes; partial dependency groups expose collection names and retain LKG revisions/digests.
- Completed bounded, filterable audit diagnostics across MySQL, application authorization, Kitex AuditService, Admin BFF, OpenAPI, and console. Queries support principal/resource/time filters and deliberately never select or expose `before_data`, `after_data`, or `metadata`; MySQL 8.0/8.4 acceptance verifies the projection.
- Completed Collection and Subscription administration across compiled domain validation, `CONFIG_ADMIN`/`CONFIG_VIEWER` authorization, MySQL CAS transactions, Kitex CatalogAdminService, Admin BFF, OpenAPI, and console. Successful metadata writes advance the bound collection revision per known environment and reuse the supported `CONFIGURATION_CHANGED` outbox envelope; revision conflicts and destructive schema changes preserve existing state.
- Completed Model administration with save-time ModelCompiler validation, non-persisting compile preview, stable issue code/path/message projection, CAS updates, Kitex/Admin BFF/OpenAPI contracts, and a console editor that retains input on conflicts.
- Completed immutable ReleaseTemplate versioning with one active version per model/release type, applicability enforcement against the compiled model, scheduling/role persistence through Goose migration `000002`, Kitex/Admin BFF/OpenAPI contracts, and history UI. MySQL 8.0/8.4 verify template version history, metadata revision propagation, audit, and supported refresh outbox events.
- `go test ./... -count=1`
- `go test -race ./internal/adminbff ./internal/outbox/... -count=1`
- `pnpm --dir web/admin-console test`
- `pnpm --dir web/admin-console typecheck`
- `pnpm --dir web/admin-console lint`
- `pnpm --dir web/admin-console build`
- `FINCONFIG_TEST_MYSQL_DSN=...mysql-8.0... go test ./internal/platform/mysql/migrations -run TestGooseMigrationUpDownUp -count=1`
- MySQL 8.0 and 8.4 `TestRealMySQLCollectionAndSubscriptionMetadataTransactions`
