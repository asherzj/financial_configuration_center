ARG ENVOY_IMAGE
FROM ${ENVOY_IMAGE}

USER root
COPY --chown=envoy:envoy kitex-grpc.yaml /etc/envoy/envoy.yaml
USER envoy

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["envoy", "-c", "/etc/envoy/envoy.yaml", "--log-level", "info"]
