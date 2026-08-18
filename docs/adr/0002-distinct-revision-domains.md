# ADR 0002: Use distinct revision domains

- Status: Accepted
- Date: 2026-08-19

## Context

The earlier design used `Revision` for the global configuration watermark, aggregate optimistic concurrency and Outbox lease CAS. Those values have different allocation, ordering and recovery semantics. Sharing one allocator would create needless contention and misleading distribution changes; using local counters everywhere would destroy the global watermark.

## Decision

Use three explicit types and field names:

- `ConfigRevision`: globally allocated only when distribution-visible facts change. Records, overlays, definitions, models, subscriptions, collection versions and change log entries use it.
- `EntityRevision`: starts at 1 and increments within one mutable aggregate. Release orders and step states use it for optimistic concurrency.
- `LeaseRevision`: increments within one Outbox row for claim, delivery and replay CAS.

Change-log cursor and Outbox sequence number remain independent identifiers. Transport fields state the expected subject, for example `expected_record_revision`, `expected_collection_revision`, `expected_order_revision` and `expected_event_revision`.

## Consequences

- Approval-only actions do not advance configuration watermarks.
- Go domain code uses distinct named types so accidental comparison requires an explicit conversion.
- Persistence columns and metrics use semantic names; a bare `revision` column is prohibited in new tables.
- A transaction that executes a visible release effect may advance both an EntityRevision and a ConfigRevision.

## Rejected alternatives

- One global allocator for all writes: simple naming, but high contention and false distribution changes.
- One local counter per row: cannot provide a comparable collection watermark.
