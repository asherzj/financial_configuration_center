# FC-007 Dynamic UI, options and sensitive access

- Status: done
- Blocked by: FC-006
- Spec: acceptance scenarios 9, 10, 11

## Outcome

One metadata contract drives query/table/forms, cross-collection options stay consistent and sensitive reveal is version-bound/audited.

## Work

- Return/render projection, operators, validation, key, auto-fill, descriptions and applicability; remove model-specific branches.
- Implement STATIC/COLLECTION ResolvedOptionSet shared by PageQuery and Release validation.
- Build dependency graph/group refresh all-or-nothing.
- Implement Access application REPEATABLE READ evaluator+audit port, BFF no-store and 60-second in-memory clearing.

## Acceptance

- ALL/ONLY_DATA dynamic journey works with no hard-coded model fields and resets on snapshot identity.
- Option group failure retains the whole old group; disabled/stale selection is rejected.
- Any stale reveal fact or audit failure returns no plaintext.

## Evidence

- Dynamic ALL/ONLY_DATA metadata, filters, forms, auto-fill, option sources and masked-field journeys: `pnpm test -- --run`, `pnpm lint`, `pnpm build`.
- Dependency group failure preserves the consumer/provider last-known-good while an independent collection publishes; verified by unit tests, `go test -race ./internal/distribution/snapshot`, and the same real-transaction test on MySQL 8.0.46 and 8.4.11.
- Release validation rejects disabled/stale STATIC and COLLECTION selections while allowing a historical disabled value to remain unchanged.
- Sensitive reveal binds server epoch, snapshot generation/instance, model, collection and effective-record revisions; role/stale/audit-failure tests return no plaintext. BFF returns `Cache-Control: no-store`; browser memory clears at server expiry with a 60-second cap.
- Full Go suite: `go test ./...`.
- Implementation commits: `f9b57c2`, `231458e`, `41afd40`, `9dedc31`, `e713f04`, `360c007`, `1f98d12`, `26210ed`, `706f105`.
