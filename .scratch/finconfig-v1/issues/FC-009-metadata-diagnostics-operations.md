# FC-009 Metadata, diagnostics and dead-letter operations

- Status: blocked
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

Not run.
