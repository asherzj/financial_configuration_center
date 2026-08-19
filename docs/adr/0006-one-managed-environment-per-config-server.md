# ADR 0006: Serve one ManagedEnvironment per Config Server instance

- Status: Accepted
- Date: 2026-08-19

## Context

Configuration records and versions are Environment-scoped, while the current SnapshotManager atomically publishes one Environment at a time. Process configuration also used `Environment` for the unrelated development/test/production safety mode. If a Config Server accepted a Hint for another Environment, a legitimate event could replace the instance's entire snapshot and violate environment isolation.

## Decision

Process configuration separates `RuntimeMode` from a required, case-sensitive `ManagedEnvironment`. One Config Server instance owns exactly one ManagedEnvironment and holds only that Environment's complete snapshot lineage. GetSnapshot, GetCollections, QueryPage, Diagnostics and refresh triggers validate the request Environment before reading authoritative data or entering a queue.

An external read for another Environment returns FailedPrecondition. A cross-Environment RefreshHint returns InvalidArgument and is never enqueued. Consumer and internal identities must also authorize the ManagedEnvironment through their Scope claims.

Deployments expose one endpoint pool per ManagedEnvironment. Admin BFF and SDK configuration select that pool before making an RPC. RuntimeMode controls process safety defaults only and never participates in business Scope evaluation.

## Consequences

- A Hint or request cannot switch the Environment of an already running snapshot manager.
- Snapshot publication remains one atomic pointer swap and needs no nested per-Environment concurrency model.
- Deployment topology has at least one Config Server pool for every served Environment.
- Cross-Environment promotion remains an explicit Control Plane operation and never a Config Server read shortcut.
- Supporting multiple ManagedEnvironments in one process would require a new ADR and separate manager/coordinator/watch state per Environment.

## Rejected alternatives

- A map of Environments inside the current SnapshotManager: its version comparisons, QueryPage path and atomic publication boundary are not multi-Environment structures.
- Trusting callers to route correctly: a valid but misrouted Hint could still replace the entire snapshot.
- Inferring ManagedEnvironment from RuntimeMode: production is a safety mode, not a stable business Scope value.
