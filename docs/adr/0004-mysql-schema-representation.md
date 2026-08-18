# ADR 0004: Freeze MySQL schema representation rules

- Status: Accepted
- Date: 2026-08-19

## Context

The logical table list did not settle collations, time range, JSON scalar representation or delete behavior. Those choices affect key equality, digest stability, future scheduling and portability between the supported MySQL versions. GORM defaults are not precise enough to serve as the schema contract.

## Decision

- IDs, codes and canonical identifiers use ASCII with `ascii_bin`.
- Human display text uses `utf8mb4_0900_as_cs`.
- Identifier constructors reject a value if trimming would change it, preventing trailing-space equality surprises.
- Domain, scheduling and audit timestamps use UTC `DATETIME(6)` rather than `TIMESTAMP(6)`.
- ConfigurationRecord JSON values are canonical strings; JSON numbers and booleans are not mixed into the stored record representation.
- Every foreign key declares `ON DELETE RESTRICT` explicitly.
- Goose SQL declares exact columns, CHECK constraints and indexes. GORM tags, AutoMigrate and hooks are not schema or domain authorities.

## Consequences

- EffectiveUntil is not limited by the 2038 TIMESTAMP range.
- MySQL 8.4 and 8.0 schema contracts can compare exact metadata.
- Domain parsing, not the database driver, owns scalar meaning.
- Display search is case-sensitive in V1; a later search policy requires an explicit design change.

## Rejected alternatives

- Uniform `utf8mb4` defaults: equality varies with server/database defaults.
- Uniform `TIMESTAMP(6)`: unsuitable for long-lived future schedules.
- Native JSON scalar types: makes canonical data/digest behavior dependent on decode choices.
