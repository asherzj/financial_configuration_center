# FC-007 Dynamic UI, options and sensitive access

- Status: blocked
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

Not run.
