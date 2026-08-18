# FinConfig V1 Executable Specification

- Status: Accepted for implementation
- Date: 2026-08-19
- Scope: first production-capable version, MySQL only

## 1. Problem

Financial applications need structured configuration that is safer than direct database editing and richer than key/value storage. Operators must query and review effective configuration, publish controlled changes, audit sensitive access and let Go services consume last-known-good data even while the control path is unavailable.

The implementation must preserve the behavior and consistency semantics described here. Directory shape is secondary; bypassing a stated invariant is not permitted.

## 2. Solution

FinConfig consists of a MySQL-backed Control Plane, a read-only Config Server with immutable snapshots, a Go SDK, an Admin BFF and a model-driven React console. Configuration writes occur only through ReleaseOrder. Config Server and SDK retain last-known-good snapshots and use ConfigRevision/digest comparison for convergence; hints and Watch are accelerators, never authority.

The selected stack is Go 1.26.6, official open-source Kitex v0.16.2 over explicitly selected standard gRPC, MySQL 8.4 with 8.0.46 compatibility, GORM v1.31.2, Goose SQL migrations, slog, OpenTelemetry Trace, Prometheus, Node 24 LTS, pnpm, React and TypeScript 5.9. The design entry is IMPLEMENTATION_READY, so these choices are binding unless an accepted ADR records a necessary compatibility correction.

## 3. Domain and consistency rules

1. Base records are Environment-scoped and identified by `(collection, environment, record_key)`; Region and Stage variations are Overlay rules.
2. `ConfigRevision`, `EntityRevision` and `LeaseRevision` are distinct named domains as specified by ADR 0002.
3. A ConfigurationSnapshot and every nested value are immutable after publication. Publication is one atomic pointer swap.
4. Snapshot identity is `(server_epoch, server_instance_id, snapshot_instance, snapshot_generation)`. Epoch changes force FULL; generation is only ordered within one instance.
5. RecordKey, secondary index keys and active conflict keys use canonical collision-free encoding. Digests use stable serialization and SHA-256.
6. Every distribution-visible transaction atomically writes its facts, ConfigRevision, CollectionVersion/digest, change log, audit and Outbox event. Network calls occur after commit.
7. RefreshHint and Watch can be lost, duplicated and reordered. Version poll guarantees eventual convergence.
8. Enabled COLLECTION OptionSource edges form dependency groups. A changed connected group refreshes all-or-nothing.
9. ReleaseOrder is the sole configuration write aggregate. A successful order is immutable and can only be reversed with a CompensatingRelease.
10. Sensitive values never enter logs, metric labels, traces, ordinary diffs or unauthorized responses.

## 4. Required behavior by actor

### 4.1 Configuration administrator

- Can create/update CollectionDefinition, Subscription and ConfigurationModel using optimistic ConfigRevision checks.
- Can create immutable versioned ReleaseTemplates and activate one per `(model, release_type)`.
- Cannot directly create, update or delete ConfigurationRecord.
- Cannot enable a model that fails ModelCompiler, references a sensitive OptionSource field or has an invalid release/template binding.
- Cannot enable an SDK Subscription when the Collection has `SDKDeliveryEnabled=false`.

### 4.2 Operator

- Selects Region, Environment, optional Stage and a model.
- First load/scope change calls QueryPage ALL and receives rows, stable projection fields, complete interaction metadata, resolved options and applicable release types.
- Query/filter/page actions call ONLY_DATA without replacing interaction metadata.
- Can add, copy, modify and delete rows into a browser-only ChangeDraft; duplicate RecordKeys merge deterministically.
- Reviews field-level diff using EffectiveBefore, BaseBefore and After, then creates a ReleaseOrder with expected record and collection revisions.
- Can execute only server-projected allowed actions; every action has an idempotency UUID.

### 4.3 Approver

- Can approve/reject only the current MANUAL_REVIEW step when role and Scope authorize it.
- Production self-approval follows the explicit managed Environment policy.
- A repeated network request with the same action ID returns the original result; a new stale request is Aborted.

### 4.4 SDK consumer

- Authenticates using Consumer JWT + TLS and can read only enabled Subscriptions whose ConsumerID equals token `sub`.
- Receives Environment-scoped base records and Scope/bucket-effective overlays.
- Builds and swaps an immutable local snapshot; queries never observe a partial update.
- Retains last-known-good on transport, decode, validation or callback failure.
- Uses Watch only as a refresh signal and version polling as self-healing.
- An enabled Subscription authorizes service-identity access to the whole Collection, including sensitive fields.

### 4.5 Auditor / sensitive viewer / platform operator

- Audit records are append-only and queryable with bounded filters.
- Sensitive viewer reveals one field only when record/collection/model revisions and snapshot identity still match; read and successful audit commit in one REPEATABLE READ transaction.
- Platform operator can inspect snapshot/collection status and replay DEAD_LETTER Outbox events using LeaseRevision CAS, reason and explicit confirmation.

## 5. QueryPage contract

ALL returns `projection_fields` plus, for every field: name/display/description, type, UI control, queryable/editable/required/sensitive/projected/key flags, auto-fill rule, all allowed filter operators, default operator, validation rules, default value and resolved options. The frontend must contain no model-specific field branches.

Page number and size are proto optional. Omitted number is 1; omitted size is model default; explicit non-positive values are InvalidArgument. Sorting is type-aware and stable with RecordKey ASC as final key. A snapshot identity change resets offset pagination to page 1.

Ordinary QueryPage applies non-percentage Overlay only. Authorized preview may provide bucket 0..99. SELECT values are validated against the current ResolvedOptionSet both during query and release creation. Disabled options may display historical data but cannot be newly submitted.

## 6. Release contract

### 6.1 Template

Every template declares one `FinalEffect`:

- `BASE_FINAL`: exactly one BASE_APPLY; rollout overlays are temporary; completion leaves no process overlay.
- `OVERLAY_FINAL`: BASE_APPLY forbidden; completion may retain the compiled final overlay.

Step params compile to the closed typed schemas in the release design. Unknown keys, scripts, SQL and expressions are invalid. Scheduling is allowed only when the template declares it and the requested window is within its maximum.

### 6.2 Item and conflicts

Each item persists BaseBefore, EffectiveBefore, After, ExpectedRecordRevision and ExpectedCollectionRevision. BASE_FINAL conflicts on `(collection, environment, record_key)`; OVERLAY_FINAL conflicts on `(collection, full scope, record_key)`.

Execution reloads and locks authority. BASE_APPLY changes only the target Environment and stores previous/applied base plus removed Overlay rules. OVERLAY_APPLY compiles the rule from current base to desired effective state. Step effects use a versioned closed union; unknown effect versions cannot be compensated.

### 6.3 Actions and recovery

All actions require `(order_id, action_request_id, normalized_request_digest)`. Same ID/same digest returns the persisted result; same ID/different digest returns `IDEMPOTENCY_KEY_REUSED`; different ID/stale EntityRevision returns Aborted. V1 does not actively transition orders to FAILED; recoverable failures keep the step/order pending and invariant corruption returns Internal plus an alert.

## 7. Authentication and authorization

- Browser: confidential OIDC Authorization Code + PKCE; validate state, nonce, issuer, audience and expiry.
- Session: 30-minute stateless AEAD HttpOnly cookie with rotating `kid`; no refresh token and no global instant revocation in V1.
- CSRF: Origin check plus session-bound HMAC double-submit token.
- BFF to RPC: Kitex client uses TLS/mTLS standard gRPC to the target Envoy sidecar plus a per-request 60-second Ed25519 JWT; Envoy forwards only over same-Pod UDS/loopback h2c to Kitex.
- SDK: TLS plus issuer/audience/JWKS-validated Consumer JWT.
- ScopePattern segments are exact or the whole segment `*`; partial glob and regex are rejected.

## 8. Persistence decisions

Goose owns exact schema, constraints and indexes; GORM AutoMigrate and hooks are prohibited. Application packages own narrow transaction ports; there is no system-wide god Tx. Identifiers are ASCII/ascii_bin, display text uses utf8mb4_0900_as_cs, domain times use UTC DATETIME(6), record JSON stores canonical strings, and all FK delete actions are explicit RESTRICT.

The migration contains the 16 logical tables listed in the persistence design, including `release_action_requests`. MySQL 8.4.11 and 8.0.46 must both pass migration and repository contracts.

## 9. Test seams and strategy

These are the agreed seams for test-driven work:

- Pure domain seam: constructors, canonical encoding, ModelCompiler, Overlay evaluator, digest, Release aggregate and compensation planning.
- Application seam: each module's public command/query interface with its own fake transaction/read ports; mocks exist only for clocks, identities, remote transports and transaction boundaries.
- MySQL contract seam: real MySQL migrations and adapter behavior, including constraints, isolation, locks, CAS and rollback injection.
- Transport seam: Kitex handlers against fake applications plus an independent grpc-go interoperability client; OpenAPI schema and BFF adapter tests.
- User seam: Playwright against the real BFF/RPC/MySQL stack for one journey per vertical slice.

For every slice: write one failing acceptance/tracer test, implement the minimal end-to-end path, then fill unit/property/fuzz/error/concurrency tests. Avoid repository-call choreography tests and in-memory substitutes for MySQL semantics.

## 10. Acceptance scenarios

1. Create an Environment-scoped base record through BASE_FINAL release; production changes while staging remains unchanged.
2. SDK starts from FULL, queries by key/index, survives Config Server outage with last-known-good, then converges after recovery.
3. Lost RefreshHint and Watch still converge through version poll.
4. Two concurrent base releases for the same Environment/key yield exactly one active order; different Environments may proceed.
5. Repeating an action ID has no duplicate effect; stale new action aborts.
6. Manual review enforces role and production self-approval policy.
7. OVERLAY_FINAL changes one full Scope and rolls back to the exact previous rule.
8. Percentage rollout selects deterministic clients, expands monotonically and promotes to base with exact cleanup.
9. QueryPage ALL dynamically renders query/table/form metadata; ONLY_DATA preserves it; snapshot change resets page.
10. COLLECTION options and their consumer refresh atomically as a dependency group.
11. Sensitive reveal aborts on any stale revision/snapshot fact and emits no plaintext when audit fails.
12. Base effect rollback restores both prior base and removed overlays; SUCCEEDED compensation creates a new order.
13. Outbox multi-worker claim, retry, lease expiry, dead-letter and replay are CAS-safe.
14. PITR simulation with a new ServerEpoch forces SDK FULL.
15. MySQL 8.4/8.0, `go test -race`, frontend unit tests and Playwright all pass without sensitive/high-cardinality telemetry.

## 11. Out of scope

PostgreSQL, multi-tenant semantics, cross-region multi-writer databases, non-Go SDKs, shared server-side drafts, external approval vendors, arbitrary SQL/JOIN/scripts, runtime plugins, broker dependency, instant global browser-session revocation and V1 FAILED-state recovery commands.

## 12. Source of truth and change control

This spec, accepted ADRs and `CONTEXT.md` override contradictory earlier prose. Proto/OpenAPI and tests become frozen once generated. A behavior change requires a reproducer, affected invariant, spec/ADR/contract update and then implementation; handlers may not add bypasses or model-specific exceptions.
