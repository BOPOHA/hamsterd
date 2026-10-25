# hamsterd shared-cache image, built by goreleaser from prebuilt binaries.
# Static base with CA certificates for upstream TLS verification; runs as the
# distroless nonroot user (65532).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/hamsterd /usr/bin/hamsterd
COPY packaging/container/config.json /etc/hamsterd/config.json
# /var/lib/hamsterd holds the generated CA and the cache; mount a volume here.
COPY --chown=65532:65532 packaging/container/state/ /var/lib/hamsterd/

VOLUME ["/var/lib/hamsterd"]
EXPOSE 8080
ENTRYPOINT ["/usr/bin/hamsterd", "-config", "/etc/hamsterd/config.json", "-cacert", "/var/lib/hamsterd/ca.crt", "-cakey", "/var/lib/hamsterd/ca.key"]
