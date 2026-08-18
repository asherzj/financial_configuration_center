# FC-002 Base-only walking skeleton

- Status: done
- Blocked by: FC-001 (done)
- Spec: acceptance scenarios 1, 2

## Outcome

One real Environment-scoped base change travels from browser to Release, MySQL, Config Server, SDK and QueryPage without direct record CRUD.

## Work

- TDD Catalog definitions/model compiler, canonical scalar/key/digest and minimal BASE_FINAL Release aggregate/executor.
- Implement narrow Catalog/Release MySQL ports and target-Environment record/version persistence.
- Implement FULL immutable snapshot, Config unary read, base-only SDK store/query and QueryPage ALL/ONLY_DATA.
- Implement minimal BFF routes and model-driven React table/add/diff/release/complete journey.

## Acceptance

- A production ADD is visible in production through PageQuery and SDK while staging remains empty.
- Restart/failing refresh retains last-known-good; no handler or repository exposes direct ConfigurationRecord mutation.

## Evidence

- `TestRealMySQLHTTPWalkingSkeleton` exercises authenticated HTTP QueryPage, Release creation/actions, the transactional MySQL apply, immutable snapshot refresh, QueryPage visibility and SDK retrieval without a direct record mutation route.
- The walking skeleton passed against MySQL 8.4.11 and MySQL 8.0.46; `TestRealMySQLBaseFinalTransaction` also verifies production-only persistence while staging remains empty.
- Snapshot and SDK suites cover last-known-good retention on failed refresh, digest validation, identity reset and callback-before-swap behavior.
- Full verification passed: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, plus web lint, typecheck, tests and production build.
