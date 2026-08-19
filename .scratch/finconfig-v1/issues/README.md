# FinConfig V1 local issue index

No repository issue tracker is configured, so these files are the implementation queue. Status values: `ready`, `blocked`, `in-progress`, `done`. Agents must update status and evidence in the owning file and may start a ticket only when every blocker is `done`.

| ID | Slice | Blockers | Status |
|---|---|---|---|
| FC-001 | Contract/toolchain spine | — | done |
| FC-002 | Base-only walking skeleton | FC-001 | done |
| FC-003 | Concurrency and durability | FC-002 | done |
| FC-004 | Manual approval workflow | FC-003 | done |
| FC-005 | Scope Overlay | FC-004 | done |
| FC-006 | Percentage rollout | FC-005 | done |
| FC-007 | Dynamic UI/options/sensitive | FC-006 | done |
| FC-008 | Rollback and compensation | FC-007 | done |
| FC-009 | Metadata/diagnostics/dead-letter | FC-008 | ready |
| FC-010 | Operations hardening/final matrix | FC-009 | blocked |
