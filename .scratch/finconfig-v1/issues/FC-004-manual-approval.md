# FC-004 Manual approval workflow

- Status: in-progress
- Blocked by: FC-003
- Spec: acceptance scenario 6

## Outcome

The complete manual-review workflow is state-machine driven, authorized and usable from the console.

## Work

- TDD typed MANUAL_REVIEW/BASE_APPLY/COMPARE/COMPLETE params and all state/action transitions.
- Implement create, execute, approve/reject, advance, complete and server-derived allowedActions.
- Enforce step roles, Scope and explicit production self-approval policy.
- Add list/detail stepper, actions, operation log and browser journey.

## Acceptance

- Every state/action pair has a test; unauthorized and self-approval paths fail without state changes.
- Browser journey creates, submits, approves, executes, advances and completes; retry uses one action ID.

## Evidence

Not run.
