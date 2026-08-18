# FC-002 Base-only walking skeleton

- Status: ready
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

Not run.
