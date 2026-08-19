# FC-008 Rollback and compensating release

- Status: in-progress
- Blocked by: FC-007
- Spec: acceptance scenario 12

## Outcome

Every mutable in-progress effect is exactly reversible and successful history is reversed only through a new reviewed order.

## Work

- Complete versioned Overlay/Base/Percent effect codecs and reject unknown versions.
- Inverse all executed effects in reverse order in one transaction.
- Generate reverse items for a current authorized compensation template and revalidate authority/revisions.
- Add rollback/compensation UI and diff lineage.

## Acceptance

- Base records and removed overlays restore byte-equivalent canonical facts after rollback.
- SUCCEEDED cannot roll back; compensation creates a linked order and refuses third-party drift.

## Evidence

Not run.
