# FC-005 Scope Overlay

- Status: blocked
- Blocked by: FC-004
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

Not run.
