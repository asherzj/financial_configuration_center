# ADR 0005: Terminate Kitex gRPC TLS at an Envoy sidecar

- Status: Accepted
- Date: 2026-08-19

## Context

FinConfig requires official open-source Kitex, standard gRPC interoperability and production TLS/mTLS. Kitex v0.16.2 supports standard gRPC and TLS on its client, but its server does not natively terminate TLS; wrapping the listener is rejected and the official FAQ documents this limitation. Replacing the server with grpc-go would violate the selected RPC framework, while exposing cross-network h2c would weaken the security boundary.

## Decision

Each RPC deployment places an Envoy sidecar in front of its Kitex server. Envoy owns the network listener, requires TLS 1.2+ with ALPN `h2`, validates client certificates and allowed SANs, and forwards standard HTTP/2 gRPC as h2c to a Kitex backend reachable only through a same-Pod Unix domain socket (preferred) or loopback fallback. The backend listener is never exposed through a Service or host port.

All FinConfig network clients connect to the destination Envoy using Kitex `WithTransportProtocol(transport.GRPC)` and `WithGRPCTLSConfig`. Application authorization still validates the short-lived internal JWT or Consumer JWT; it never treats proxy reachability or XFCC alone as authorization. If peer identity is forwarded, Envoy uses a sanitize-and-set policy and the Kitex backend accepts it only from the private backend channel.

The transport contract test may use a small in-process TLS terminator to prove TLS/ALPN and grpc-go interoperability, but that proxy is test-only. Production and compose deployments use a pinned Envoy image and validated static configuration.

The application owns the backend Unix socket lifecycle through an explicitly constructed listener. The path is a canonical absolute path under `/var/run/finconfig/`; the parent and target may not be symlinks. Startup rejects a non-socket target or a socket that accepts connections. It removes a stale socket only after a connection attempt proves `ECONNREFUSED`, then binds, applies the configured mode/group and gives the listener to Kitex. Shutdown removes only the socket created by this process and only while its filesystem identity still matches.

Envoy's localhost-only admin endpoint is part of the shutdown trust boundary. The application marks itself unready, asks that endpoint to drain listeners, closes long-lived Watch streams, and then stops Kitex. The Envoy admin address is not user-provided per request and is never exposed outside the Pod.

## Consequences

- Kitex remains the RPC framework while the externally reachable endpoint is standard TLS/mTLS gRPC.
- Deployment health must distinguish Envoy listener readiness from Kitex backend readiness.
- Certificate rotation, SAN policy and downstream TLS metrics live at the sidecar boundary.
- No cross-host traffic is permitted between Envoy and Kitex in cleartext.
- Readiness waits for the managed socket to be bound and Kitex to enter its accept loop; a generation-zero placeholder is not ready.
- Socket ownership and Envoy drain behavior require Unix and compose integration tests in addition to static configuration tests.

## Rejected alternatives

- grpc-go server: interoperable and TLS-capable, but violates the explicit Kitex choice.
- Cross-network TLS termination at a shared load balancer followed by h2c: leaves an unencrypted network hop.
- A custom Go TLS proxy in production: duplicates mature proxy security, rotation and observability behavior.
