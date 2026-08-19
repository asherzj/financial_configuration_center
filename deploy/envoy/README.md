# Kitex gRPC mTLS sidecar

This image is the mandatory network boundary for every FinConfig Kitex server. The application container and Envoy share `/var/run/finconfig`; Kitex listens only on `backend.sock`, while Envoy exposes port 8443 with mTLS and standard HTTP/2 gRPC.

Build with an explicitly reviewed, digest-pinned Envoy base image:

```sh
docker build \
  --build-arg ENVOY_IMAGE=envoyproxy/envoy@sha256:<reviewed-digest> \
  -t finconfig-envoy deploy/envoy
```

Mount `tls.crt`, `tls.key`, and `ca.crt` read-only at `/etc/finconfig/tls`. Client certificates must carry the exact DNS SAN `finconfig-internal-client`. The Envoy admin listener is loopback-only and must not be published.

The backend socket must never be mounted into unrelated workloads or exposed through a Service/host port. Application authorization still verifies a short-lived internal JWT or Consumer JWT; the forwarded client certificate is diagnostic context only.
