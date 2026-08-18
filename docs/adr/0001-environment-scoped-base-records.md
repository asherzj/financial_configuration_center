# ADR 0001: Scope base records by environment

- Status: Accepted
- Date: 2026-08-19

## Context

FinConfig promises Environment isolation. The earlier model stored one global base record while versions and releases were Environment-scoped. A production `BASE_APPLY` would therefore also change staging and test, and the persisted base digest could not describe an Environment-specific fact.

## Decision

`ConfigurationRecord` is scoped by `(collection_name, environment, record_key)`. Base reads, digests, optimistic checks, conflicts, release effects, snapshots and SDK delivery always select one Environment. Every configured Environment has a `configuration_versions` row for each collection. Region and Stage remain Overlay dimensions.

The runtime configuration contains a non-empty allow-list of managed Environments. Collection creation initializes empty version rows for every configured Environment. Removing an Environment is an explicit operational migration, not an implicit configuration change.

## Consequences

- A base release cannot leak into another Environment.
- The base primary key and all relevant foreign/reference indexes include Environment.
- A BASE_FINAL active conflict key is `collection + environment + record_key`.
- Data may be duplicated when Environments intentionally share values; V1 accepts this in exchange for isolation.
- Cross-Environment promotion is represented by a new ReleaseOrder using copied desired values, not shared mutable base state.

## Rejected alternatives

- Global base plus Environment overlays: makes the normal production path an Overlay and keeps BASE_APPLY globally dangerous.
- Clone-on-first-write: creates surprising inheritance and makes digests dependent on hidden ancestry.
