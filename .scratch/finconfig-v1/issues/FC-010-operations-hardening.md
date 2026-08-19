# FC-010 Operations hardening and final matrix

- Status: in-progress
- Blocked by: FC-009
- Spec: sections 7-12, acceptance scenarios 14-15

## Outcome

The entire V1 runs securely and observably, survives lifecycle faults and is documented from verified commands.

## Work

- Implement OIDC/session/CSRF, mTLS/internal JWT, Consumer JWT/JWKS and exact ScopePattern semantics.
- Add slog/OTel/Prometheus with sensitive/high-cardinality guards, health/readiness and graceful shutdown.
- Add Dockerfiles/compose, seed, examples, capacity/failure injection and PITR epoch simulation.
- Run MySQL 8.4/8.0, unit/property/fuzz/contracts, race, frontend and Playwright matrices.
- Run independent Standards and Spec reviews from a fixed base, fix findings and do final docs pass.

## Acceptance

- `docs/design/11-testing-and-delivery.md` Definition of Done is fully evidenced.
- Epoch change forces FULL; no secrets/config bodies/high-cardinality labels are emitted; shutdown leaks no goroutines.
- README/runbooks reproduce a clean setup and all supported journeys.

## Evidence

- Added exact whole-segment ScopePattern matching; partial glob syntax is rejected.
- Added Ed25519 JWT signing/verification with strict algorithm/key ID, issuer/audience/lifetime/JTI validation, 60-second internal-token enforcement, and Consumer subject/ClientID binding.
- Added rotating AES-256-GCM session cookies plus session-bound double-submit CSRF and exact HTTPS Origin validation; Admin BFF session authentication enforces CSRF on every unsafe method.
- Added a private Prometheus registry with the full V1 metric surface and constructor-time bounded vocabularies; every recording method rejects unknown dynamic labels before a series can be created.
- Added a recursive slog JSON redaction handler for credentials, tokens, cookies, configuration payloads, before/after data and nested groups, including defensive URI/Bearer/JWT scrubbing for error text.
- Added an injected OpenTelemetry trace provider with service resource attributes, parent-based ratio sampling and W3C TraceContext/Baggage propagation without mutating global providers.
- Added payload-free `/healthz`, bounded `/readyz`, private `/metrics`, an atomic readiness gate and ordered global-timeout shutdown phases (stop, cancel, drain, flush, close).
- Verified with `go test ./...` and `go test -race ./internal/platform/observability ./internal/platform/health ./internal/platform/lifecycle`.
- Added the production browser OIDC Authorization Code + S256 PKCE flow with an AEAD-sealed, short-lived login transaction; callback validation binds state, nonce, redirect URI and code verifier before issuing the existing rotating session/CSRF cookies.
- Added strict RS256 OIDC ID-token verification (issuer, audience/azp, subject, nonce, iat, exp), bounded HTTPS JWKS loading/caching with key rotation, a confidential token endpoint client, payload-free auth errors, logout and session routes.
- Added CSP, HSTS, nosniff, no-referrer and frame denial headers to all Admin BFF responses; updated the OpenAPI authentication/session contract.
- Verified OIDC/auth with `go test ./...` and `go test -race ./internal/platform/auth ./internal/adminbff`.
- Added the accepted Envoy/Kitex production transport boundary: Envoy requires client certificates, TLS 1.2+, ALPN h2 and an exact client SAN, sanitizes forwarded certificate identity, and uses explicit HTTP/2 h2c only over `/var/run/finconfig/backend.sock`.
- Added Kitex server options that reject TCP, temporary and path-traversal backend addresses; added an Envoy sidecar Dockerfile that requires the release pipeline to supply a reviewed digest-pinned base image.
- Verified the static deployment boundary and Kitex options with `go test ./internal/platform/rpc ./deploy/envoy`.
- Added an idempotent `cmd/seed` that uses the production Catalog application service and GORM adapter to create a usable payment-route Collection, dynamic model, SDK subscription and reviewed direct-release template without bypassing ReleaseOrder for record data.
- Verified migration plus two consecutive seed runs on MySQL 8.4.11 and 8.0.46; each database retained exactly one Collection, Model, Subscription and Template. The isolated acceptance databases were removed afterwards.
