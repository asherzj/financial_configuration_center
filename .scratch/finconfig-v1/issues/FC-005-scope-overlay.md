# FC-005 Scope Overlay

- Status: done
- Blocked by: FC-004 (done)
- Spec: acceptance scenario 7

## Outcome

OVERLAY_FINAL produces a Scope-specific effective record and exact reversible effects.

## Work

- TDD environment then exact-stage precedence, action invariants and stable overlay digest.
- Compile desired effective state relative to base into ADD/MODIFY/DELETE Overlay rules.
- Persist versioned previous/new OverlayStepEffect and implement inverse compensation.
- Add Scope controls and effective/base diff to QueryPage and UI.

## Acceptance

- Two full Scopes observe expected distinct effective values from one base.
- IN_PROGRESS rollback restores the exact prior rule or absence and advances version/audit/outbox atomically.

## Evidence

- `go test ./...` passes.
- `go vet ./...` passes.
- Real MySQL 8.0.46 and 8.4.11 pass `TestRealMySQLOverlayApplyAndRollbackTransaction`, including database-to-snapshot-to-QueryPage effective/base verification and exact rollback.
- `pnpm test`, `pnpm lint`, `pnpm typecheck`, and `pnpm build` pass.
- Domain, compiler, release effect, persistence, RPC/BFF and read-side commits: `609f50d`, `a0a479a`, `644dc69`, `a4541c0`, `ba904cc`, `84b6533`, `1880308`.
