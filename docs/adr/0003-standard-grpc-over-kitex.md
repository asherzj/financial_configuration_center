# ADR 0003: Use standard gRPC transport explicitly with Kitex

- Status: Accepted
- Date: 2026-08-19

## Context

Kitex Protobuf services can use Kitex's own Protobuf transport or standard gRPC. Relying on generator or streaming defaults makes unary interoperability ambiguous and could let two services pass internal tests while failing with a standard gRPC client.

## Decision

All FinConfig RPC definitions use proto3. Every Kitex client explicitly selects standard gRPC transport. Servers register multiple services through Kitex and may retain protocol detection, but acceptance tests only recognize standard gRPC behavior. `Watch` is server streaming.

An independently generated grpc-go client must pass unary, streaming and structured gRPC status-detail interoperability tests against each server. TLS and mTLS tests exercise the same standard gRPC path. `go_package` always uses the complete module import path.

## Consequences

- FinConfig remains interoperable with non-Kitex gRPC clients.
- Transport choice is visible at every client composition root.
- A smoke test, not framework inference, guards the decision.
- Kitex remains the server and internal RPC framework required by the product.

## Rejected alternatives

- Kitex-Protobuf default transport: does not meet the standard gRPC interoperability requirement.
- grpc-go servers: interoperable, but violates the selected RPC framework.
