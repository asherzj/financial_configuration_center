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
