# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

# Build the existing test-service releases from verified upstream source: the
# community MinIO server and client images are no longer publicly available.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.25.13-alpine3.24@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946 AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off
ARG TARGETOS=linux
ARG TARGETARCH

# MinIO RELEASE.2025-09-07T16-13-09Z (upstream signed tag).
ADD --checksum=sha256:8819e3e7817e46b7b3798f8f200ead208562e571563c2e040352378031abe9f2 \
    https://codeload.github.com/minio/minio/tar.gz/07c3a429bfed433e49018cb0f78a52145d4bedeb /tmp/minio.tar.gz
RUN mkdir -p /src/minio /out/data \
    && tar -xzf /tmp/minio.tar.gz -C /src/minio --strip-components=1
WORKDIR /src/minio
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download \
    && go mod verify \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -mod=readonly -buildvcs=false -trimpath -tags kqueue \
      -ldflags="-s -w -X github.com/minio/minio/cmd.Version=2025-09-07T16:13:09Z -X github.com/minio/minio/cmd.ReleaseTag=DEVELOPMENT.2025-09-07T16-13-09Z -X github.com/minio/minio/cmd.CommitID=07c3a429bfed433e49018cb0f78a52145d4bedeb -X github.com/minio/minio/cmd.ShortCommitID=07c3a429bfed4 -X github.com/minio/minio/cmd.CopyrightYear=2025" \
      -o /out/minio .

# mc RELEASE.2025-08-13T08-35-41Z (upstream signed tag).
ADD --checksum=sha256:95cd293c7119f16921a6dc515a1fb74a2227f19fd994b9c8b770a154e802ac44 \
    https://codeload.github.com/minio/mc/tar.gz/7394ce0dd2a80935aded936b09fa12cbb3cb8096 /tmp/mc.tar.gz
RUN mkdir -p /src/mc \
    && tar -xzf /tmp/mc.tar.gz -C /src/mc --strip-components=1
WORKDIR /src/mc
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download \
    && go mod verify \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -mod=readonly -buildvcs=false -trimpath -tags kqueue \
      -ldflags="-s -w -X github.com/minio/mc/cmd.Version=2025-08-13T08:35:41Z -X github.com/minio/mc/cmd.ReleaseTag=DEVELOPMENT.2025-08-13T08-35-41Z -X github.com/minio/mc/cmd.CommitID=7394ce0dd2a80935aded936b09fa12cbb3cb8096 -X github.com/minio/mc/cmd.ShortCommitID=7394ce0dd2a80 -X github.com/minio/mc/cmd.CopyrightYear=2025" \
      -o /out/mc .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
LABEL org.opencontainers.image.title="Aginex MinIO test service" \
      org.opencontainers.image.description="Pinned upstream MinIO and mc source builds for local development and CI" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.source="https://github.com/minio/minio" \
      org.opencontainers.image.revision="07c3a429bfed433e49018cb0f78a52145d4bedeb"
ENV HOME=/tmp MC_CONFIG_DIR=/tmp/.mc
COPY --from=build /out/minio /out/mc /usr/local/bin/
COPY --from=build /src/minio/LICENSE /src/minio/CREDITS /licenses/minio/
COPY --from=build /src/mc/LICENSE /src/mc/CREDITS /licenses/mc/
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
EXPOSE 9000 9001
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/minio"]
CMD ["server", "/data", "--address", ":9000"]
