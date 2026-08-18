# FC-001 Contract/toolchain spine

- Status: done
- Blocked by: none
- Spec: sections 2, 7, 8, 9; ADR 0003

## Outcome

Reproducible Go/pnpm workspace, frozen proto/OpenAPI generation, Kitex standard-gRPC smoke and real-MySQL migration harness.

## Work

- Pin Go 1.26.6/backend and Node 24/pnpm/frontend toolchains and dependencies.
- Define proto3 packages/services/errors and Admin OpenAPI from the canonical model.
- Generate Kitex code; clients explicitly select gRPC. Add independent grpc-go unary/Watch/status-details smoke.
- Add 16-table Goose migration with exact MySQL 8.4/8.0 contracts, GORM adapter shell and application-owned transaction seams.
- Add lint/test/build/generate targets and CI matrix; generation twice must be clean.

## Acceptance

- Toolchain, proto/OpenAPI lint, deterministic generation and empty build pass.
- MySQL 8.4.11 and 8.0.46 execute up/down/up and schema assertions.
- TLS standard-gRPC unary + stream smoke passes without Kitex private transport.

## Evidence

- `go test ./...`, `go test -race ./...`, and `go build ./...` pass with Go 1.26.6.
- Buf lint and format checks pass; two consecutive Go/Kitex/OpenAPI generations compare byte-for-byte equal.
- Frontend lint, TypeScript check, Vitest, and production build pass with Node 24/pnpm 11.4.0.
- Real MySQL 8.4.11 and 8.0.46 both pass up/down/up, exact 16-table, key, revision-column, and JSON/check-constraint assertions.
- Independent grpc-go reaches Kitex standard gRPC through verified TLS for unary, server-streaming, and structured status details.
- GORM opens/pings through an explicit adapter without AutoMigrate; production Kitex TLS termination is fixed by ADR 0005.
