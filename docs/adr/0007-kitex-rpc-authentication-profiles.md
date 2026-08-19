# ADR 0007: Enforce method-specific authentication at the Kitex boundary

- Status: Accepted
- Date: 2026-08-19

## Context

Config Server exposes Consumer SDK reads, internal page queries, refresh notifications and operator diagnostics through one standard-gRPC Kitex server. Consumer credentials are externally issued RS256 JWTs discovered through JWKS; internal service credentials are 60-second Ed25519 JWTs. The existing handlers must not trust request-body identity, and Kitex unary and streaming middleware take different paths unless configured explicitly.

## Decision

One Kitex server registers ConfigService, PageQueryService, RefreshService and DiagnosticsService. It enables unary compatibility middleware and installs both unary and stream authentication/observability middleware using one method-policy registry.

The policy is:

| RPC | Profile | Required binding |
|---|---|---|
| ConfigService GetSnapshot, DiffVersions, GetCollections and Watch | Consumer JWT | token subject equals request ConsumerID; token Scope covers request Scope |
| PageQueryService methods | Internal JWT | CONFIG_VIEWER or configured diagnostic role; Scope covers request Scope |
| RefreshService Notify | Internal JWT | caller subject is an allowed Control Plane relay; Scope covers ManagedEnvironment |
| DiagnosticsService methods | Internal JWT | PLATFORM_OPERATOR or AUDITOR; Scope covers ManagedEnvironment |

Consumer JWT verification accepts RS256 only, validates issuer, audience, kid and lifetime, and resolves keys from a bounded HTTPS JWKS cache. Audience may use the standard string or string-array form. An unknown kid triggers one singleflight refresh; a bounded stale-if-error window may use a previously verified cached key. Consumer identity is `sub`; SDK ClientID is an installation rollout identifier and is not a credential identity claim.

Internal JWT verification remains Ed25519 with a configured public-key ring, strict issuer/audience/algorithm/kid/lifetime/JTI checks and a maximum 60-second lifetime.

Authorization metadata must contain exactly one bounded `Bearer <token>` value. Middleware stores a typed `ConsumerIdentity` or `InternalCallerIdentity` behind a private context key. Handlers bind that identity to business request fields before invoking application services. Domain and application packages never parse transport metadata.

Envoy mTLS authenticates the network peer boundary but is not a substitute for application JWT authorization. XFCC or other forwarded certificate metadata is never a handler authorization credential.

## Consequences

- Consumer tokens cannot call internal methods, and internal tokens cannot impersonate a Consumer RPC.
- Watch authentication receives the same policy coverage as unary methods and requires a transport integration test.
- RS256 Consumer JWKS and Ed25519 internal key rings remain distinct adapters rather than a misleading common resolver.
- Errors preserve gRPC semantics: missing/invalid credentials are Unauthenticated; identity or Scope mismatch is PermissionDenied.

## Rejected alternatives

- One JWT verifier and key resolver for both profiles: RSA JWKS and Ed25519 key rings have different trust, availability and lifetime requirements.
- Handler-only token parsing: duplicates transport code and makes Watch coverage easy to omit.
- Trusting request ConsumerID or Envoy reachability: neither proves the caller owns that identity or Scope.
