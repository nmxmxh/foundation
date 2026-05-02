# syntax=docker/dockerfile:1.7

# =============================================================================
# Multi-stage Dockerfile for Ovasabi Foundation Go Projects
# =============================================================================
# Caching model:
# - go.mod/go.sum and foundation module manifests are copied before source code.
# - Named BuildKit caches are shared across all foundation projects by default.
# - Source changes rebuild the binary, but do not re-download Go modules.
# =============================================================================

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.23
ARG CACHE_NAMESPACE=ovasabi-foundation-v1

# --- Shared Go Base ---
FROM golang:${GO_VERSION}-alpine AS go-base

ARG CACHE_NAMESPACE

WORKDIR /app

ENV CGO_ENABLED=0 \
    GOCACHE=/root/.cache/go-build \
    GOMODCACHE=/go/pkg/mod \
    GOPROXY=https://proxy.golang.org,direct

RUN --mount=type=cache,id=${CACHE_NAMESPACE}-apk,target=/var/cache/apk,sharing=locked \
    apk add --update-cache git make ca-certificates tzdata

# --- Dependency Cache Stage ---
FROM go-base AS go-deps

ARG CACHE_NAMESPACE

# Copy dependency manifests only. Keep this list narrow so ordinary source edits
# do not invalidate the module-download layer.
COPY go.mod ./
COPY go.sum ./
COPY foundation/server-kit/go/go.mod ./foundation/server-kit/go/
COPY foundation/server-kit/go/go.sum ./foundation/server-kit/go/
COPY foundation/runtime-transport/go/go.mod ./foundation/runtime-transport/go/
COPY foundation/runtime-transport/go/go.sum ./foundation/runtime-transport/go/
COPY foundation/config-contracts/go/go.mod ./foundation/config-contracts/go/

RUN --mount=type=cache,id=${CACHE_NAMESPACE}-gomod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=${CACHE_NAMESPACE}-gobuild,target=/root/.cache/go-build,sharing=locked \
    go mod download && touch /tmp/deps-ready

# --- Build Stage ---
FROM go-base AS builder

ARG CACHE_NAMESPACE
ARG PROJECT_NAME=server
ARG VERSION=dev
ARG CGO_ENABLED=0

COPY --from=go-deps /tmp/deps-ready /tmp/deps-ready

# Copy source code after dependency resolution so this layer is the normal
# invalidation boundary for application changes.
COPY . .

# Build the application. The named caches are shared with go-deps and with other
# foundation projects on the same Docker builder.
RUN --mount=type=cache,id=${CACHE_NAMESPACE}-gomod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=${CACHE_NAMESPACE}-gobuild,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=${CGO_ENABLED} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /bin/${PROJECT_NAME} ./cmd/server

# --- Final Stage ---
FROM alpine:${ALPINE_VERSION} AS final

ARG CACHE_NAMESPACE
ARG PROJECT_NAME=server

WORKDIR /srv

RUN --mount=type=cache,id=${CACHE_NAMESPACE}-apk-final,target=/var/cache/apk,sharing=locked \
    apk add --update-cache ca-certificates tzdata

RUN addgroup -S app && adduser -S app -G app

COPY --from=builder /bin/${PROJECT_NAME} ./server

# Copy additional assets if the application needs them.
# COPY --from=builder /app/assets ./assets

RUN chown -R app:app /srv

USER app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/healthz || exit 1

CMD ["./server"]

# =============================================================================
# Optional: Frontend Builder Stage
# =============================================================================
# If a project chooses to build frontend assets in Docker, keep foundation local
# package dependencies in the build context and copy their manifests before
# `npm ci` so @ovasabi/* file dependencies resolve deterministically.
#
# FROM node:22-alpine AS frontend-builder
# ARG CACHE_NAMESPACE
# WORKDIR /app/frontend
# COPY frontend/package.json frontend/package-lock.json* ./
# COPY foundation/runtime-transport/ts/package.json ../foundation/runtime-transport/ts/
# COPY foundation/frontend-kit/ts/package.json ../foundation/frontend-kit/ts/
# COPY foundation/ui-minimal/ts/package.json ../foundation/ui-minimal/ts/
# RUN --mount=type=cache,id=${CACHE_NAMESPACE}-npm,target=/root/.npm,sharing=locked \
#     npm ci
# COPY foundation/runtime-transport/ts ../foundation/runtime-transport/ts
# COPY foundation/frontend-kit/ts ../foundation/frontend-kit/ts
# COPY foundation/ui-minimal/ts ../foundation/ui-minimal/ts
# COPY frontend/ .
# RUN npm run build
