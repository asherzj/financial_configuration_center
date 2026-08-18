# FC-004 Manual approval workflow

- Status: done
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

- Typed template compilation rejects unknown params, duplicate codes, missing approval roles, invalid self-approval policies and malformed BASE_FINAL sequences.
- Domain/application tests cover submit, authorized approve/reject, production self-approval denial, stale EntityRevision, stable step-code checks and action-ID replay without state drift.
- Real MySQL 8.4.11 and 8.0.46 journeys persist approval state, complete the full review/apply/complete sequence, keep failed self-approval side-effect free and write one operation log per successful action.
- Admin BFF and Kitex gRPC map roles, comments, permission errors, full template step lists and server-derived allowedActions.
- The React journey covers release-type selection, create, submit, approve with comment, advance, BASE_APPLY, COMPLETE, terminal refresh and dynamic template-snapshot step rendering.
- Full verification passed: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, frontend lint/typecheck/tests/production build, OpenAPI contract tests and browser layout/error-state inspection.
