# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM docker.io/library/golang:1.27.1-alpine3.24@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS go-build
WORKDIR /src
COPY server/go.mod server/go.sum ./server/
WORKDIR /src/server
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download \
    && go mod verify
COPY server ./
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/xgtian-root/aginex/server/internal/buildinfo.Version=${VERSION} -X github.com/xgtian-root/aginex/server/internal/buildinfo.Commit=${COMMIT} -X github.com/xgtian-root/aginex/server/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/aginex-api ./cmd/server \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/xgtian-root/aginex/server/internal/buildinfo.Version=${VERSION} -X github.com/xgtian-root/aginex/server/internal/buildinfo.Commit=${COMMIT} -X github.com/xgtian-root/aginex/server/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/aginex-worker ./cmd/worker \
    && mkdir -p /out/runtime-data/uploads

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS go-runtime
WORKDIR /app
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Aginex" \
      org.opencontainers.image.description="Production Aginex modular monolith runtime" \
      org.opencontainers.image.source="https://github.com/xgtian-root/aginex" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"
ENV HOME=/tmp \
    TMPDIR=/tmp \
    AGINEX_CONFIG_FILE=/data/aginex-config.json \
    AGINEX_STORAGE_LOCAL_ROOT=/data/uploads
COPY --from=go-build --chown=65532:65532 /out/runtime-data /data
VOLUME ["/data"]
USER 65532:65532
STOPSIGNAL SIGTERM

FROM go-runtime AS api
COPY --from=go-build --chown=65532:65532 /out/aginex-api /app/aginex-api
EXPOSE 8080
ENTRYPOINT ["/app/aginex-api"]

FROM go-runtime AS worker
COPY --from=go-build --chown=65532:65532 /out/aginex-worker /app/aginex-worker
ENTRYPOINT ["/app/aginex-worker"]

FROM docker.io/library/node:26.8-alpine3.24@sha256:ef24c5053d50fdc3e4e56eb4e7ddb7861874ab0fdc797046ba897581deb8e868 AS web-build
WORKDIR /src
ENV NEXT_TELEMETRY_DISABLED=1
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY admin/package.json admin/package.json
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY admin admin
COPY docs/openapi.json docs/openapi.json
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL}
RUN pnpm --filter @aginex/admin build \
    && test -f /src/admin/.next/standalone/admin/server.js \
    && mkdir -p /runtime/next-cache

FROM docker.io/library/node:26.8-alpine3.24@sha256:ef24c5053d50fdc3e4e56eb4e7ddb7861874ab0fdc797046ba897581deb8e868 AS web
WORKDIR /app
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Aginex Web" \
      org.opencontainers.image.description="Aginex administration web application" \
      org.opencontainers.image.source="https://github.com/xgtian-root/aginex" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    HOSTNAME=0.0.0.0 \
    PORT=3000 \
    HOME=/tmp \
    TMPDIR=/tmp
COPY --from=web-build --chown=1000:1000 /src/admin/.next/standalone ./
COPY --from=web-build --chown=1000:1000 /src/admin/.next/static ./admin/.next/static
COPY --from=web-build --chown=1000:1000 /runtime/next-cache ./admin/.next/cache
USER 1000:1000
EXPOSE 3000
STOPSIGNAL SIGTERM
CMD ["node", "admin/server.js"]
