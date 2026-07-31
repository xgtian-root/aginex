# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM docker.io/library/golang:1.25.12-alpine3.24@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download \
    && go mod verify
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/xgtian-root/aginex/internal/buildinfo.Version=${VERSION} -X github.com/xgtian-root/aginex/internal/buildinfo.Commit=${COMMIT} -X github.com/xgtian-root/aginex/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/aginex-api ./cmd/server \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/xgtian-root/aginex/internal/buildinfo.Version=${VERSION} -X github.com/xgtian-root/aginex/internal/buildinfo.Commit=${COMMIT} -X github.com/xgtian-root/aginex/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/aginex-worker ./cmd/worker \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/xgtian-root/aginex/internal/buildinfo.Version=${VERSION} -X github.com/xgtian-root/aginex/internal/buildinfo.Commit=${COMMIT} -X github.com/xgtian-root/aginex/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/aginex ./cmd/aginex \
    && mkdir -p /out/runtime-data/uploads

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35 AS go-runtime
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
    AGINEX_DATABASE_DSN=/data/aginex.db \
    AGINEX_STORAGE_LOCAL_ROOT=/data/uploads
COPY --from=go-build --chown=65532:65532 /out/runtime-data /data
USER 65532:65532
STOPSIGNAL SIGTERM

FROM go-runtime AS api
COPY --from=go-build --chown=65532:65532 /out/aginex-api /app/aginex-api
EXPOSE 8080
ENTRYPOINT ["/app/aginex-api"]

FROM go-runtime AS worker
COPY --from=go-build --chown=65532:65532 /out/aginex-worker /app/aginex-worker
ENTRYPOINT ["/app/aginex-worker"]

FROM go-runtime AS migrate
COPY --from=go-build --chown=65532:65532 /out/aginex /app/aginex
ENTRYPOINT ["/app/aginex"]
CMD ["migrate", "status"]

FROM docker.io/library/node:22.23.2-alpine3.24@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS web-build
WORKDIR /src
ENV NEXT_TELEMETRY_DISABLED=1
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json apps/web/package.json
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY apps/web apps/web
COPY docs/openapi.json docs/openapi.json
ARG NEXT_PUBLIC_API_URL=http://localhost:8080
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL}
RUN pnpm --filter @aginex/web build \
    && test -f /src/apps/web/.next/standalone/apps/web/server.js \
    && mkdir -p /runtime/next-cache

FROM docker.io/library/node:22.23.2-alpine3.24@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS web
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
COPY --from=web-build --chown=1000:1000 /src/apps/web/.next/standalone ./
COPY --from=web-build --chown=1000:1000 /src/apps/web/.next/static ./apps/web/.next/static
COPY --from=web-build --chown=1000:1000 /runtime/next-cache ./apps/web/.next/cache
USER 1000:1000
EXPOSE 3000
STOPSIGNAL SIGTERM
CMD ["node", "apps/web/server.js"]
