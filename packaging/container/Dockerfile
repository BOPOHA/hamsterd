# hamsterd shared-cache image, built by goreleaser from prebuilt binaries.
# Static base with CA certificates for upstream TLS verification; runs as the
# distroless nonroot user (65532).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/hamsterd /usr/bin/hamsterd
COPY packaging/container/config.json /etc/hamsterd/config.json
# /var/lib/hamsterd holds the generated CA and the cache; mount a volume here.
COPY --chown=65532:65532 packaging/container/state/ /var/lib/hamsterd/

VOLUME ["/var/lib/hamsterd"]
EXPOSE 8080
ENTRYPOINT ["/usr/bin/hamsterd", "-config", "/etc/hamsterd/config.json", "-cacert", "/var/lib/hamsterd/ca.crt", "-cakey", "/var/lib/hamsterd/ca.key"]
