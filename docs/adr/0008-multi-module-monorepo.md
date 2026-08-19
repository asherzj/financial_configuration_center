# ADR 0008: Organize FinConfig as a multi-module monorepo

- Status: Accepted
- Date: 2026-08-19

## Context

The original repository layout used one root Go module and grouped code by system-wide capabilities under `internal/`. That made early consistency work convenient, but it did not make the four independently delivered products—Admin, Config Server, Go Client SDK, and Frontend—visible ownership boundaries. Composition code and adapters could grow around global packages, and a product could accidentally import another product's implementation.

The Go SDK also needs an independently versioned public import path. Admin and Config Server need independent build, test, dependency, and release lifecycles even though they share wire contracts and a small set of technical policies.

## Decision

FinConfig is a multi-module monorepo. The repository root is not a Go module. It contains a committed `go.work` for local development and orchestration only.

The product modules are:

- `github.com/asherzj/financial_configuration_center/admin`
- `github.com/asherzj/financial_configuration_center/server`
- `github.com/asherzj/financial_configuration_center/client_sdk`
- the independent pnpm package `@finconfig/admin-console` located at `frontend/`

Two non-product Go modules provide narrow shared seams:

- `github.com/asherzj/financial_configuration_center/contracts` owns protobuf, OpenAPI source, generated Go transport bindings, and the versioned MySQL schema compatibility manifest. It contains no domain behavior.
- `github.com/asherzj/financial_configuration_center/platform` owns reusable technical primitives such as process lifecycle, observability, generic JWT verification, RPC transport, and database connection mechanics. It contains no Catalog, Release, Snapshot, Query, Watch, or SDK policy.

Admin, Server, and Client SDK may import Contracts. Admin and Server may import Platform. No product module may import another product module. Client SDK deliberately does not import Platform so that its public dependency graph remains small and under its own control.

Each Go product module owns its own `go.mod` and `go.sum`. Production module files contain no local `replace` directives. The root `go.work` selects local module versions during repository development; consumers and release builds resolve tagged module versions normally.

Admin, Server, and Client SDK are organized by bounded context. Within a bounded context, dependencies point from interfaces and infrastructure toward application-owned seams and domain types. Domain code imports neither Kitex/protobuf nor GORM/MySQL nor concrete logging packages. Product composition roots live inside the owning module and are the only locations allowed to assemble domain, application, infrastructure, and transport adapters.

Frontend is an independent Node/pnpm package. It communicates only with Admin BFF through the versioned OpenAPI contract and never imports Go-generated implementation packages or calls Config Server directly.

Module releases use path-prefixed tags such as `server/v0.3.0` and `client_sdk/v0.3.0`. A Contracts or Platform change is consumed by explicit version updates in dependent `go.mod` files. CI tests every module independently and also tests the complete root workspace.

## Consequences

- Ownership, build output, dependency updates, and release version are explicit per product.
- Go `internal` visibility prevents consumers from reaching product implementations, while the Client SDK root package remains a deliberate public facade.
- Cross-product behavior must travel through protobuf/OpenAPI/database compatibility contracts instead of implementation imports.
- Some technical code may be duplicated when sharing it would leak product policy; reducing duplication is subordinate to preserving module independence.
- Changes spanning Contracts or Platform and a product require ordered, independently valid releases: publish and tag the backward-compatible support-module change first, then update each dependent product's exact required version in a later commit. A local workspace build is not evidence that a releasable module dependency exists.
- The existing single-module tree must be migrated before further feature work adds new ownership to obsolete global paths.

## Rejected alternatives

- One Go module with four top-level directories: directory naming alone cannot provide independent dependency or release lifecycles.
- A module per bounded context: it turns internal DDD seams into repository-wide versioning overhead and makes application transactions harder to keep local.
- A single shared domain module: Catalog, Release, Snapshot, and SDK models have different ownership and consistency needs; sharing them would couple products through implementation types.
- Letting Client SDK import Server packages: it exposes server implementation and prevents the SDK from maintaining its own compact local model.
