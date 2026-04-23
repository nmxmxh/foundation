# =============================================================================
# Multi-stage Dockerfile for Ovasabi Foundation Go Projects
# =============================================================================
# Customize:
# - PROJECT_NAME: Your project name
# - Add/remove build dependencies as needed
# - Uncomment frontend stages if building a full-stack app
# =============================================================================

# --- Build Stage ---
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Build arguments
ARG PROJECT_NAME=server
ARG CGO_ENABLED=0

# Set GOPROXY for reliability
ENV GOPROXY=https://proxy.golang.org,direct

# Install build dependencies
# Uncomment additional dependencies as needed:
# - protobuf protobuf-dev: for protobuf generation
# - tesseract-ocr-dev leptonica-dev: for OCR support
# - gcc g++ musl-dev: for CGO builds
RUN apk add --no-cache git make ca-certificates tzdata

# Uncomment for protobuf support
# RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
#     go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Cache dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy source code
COPY . .

# Generate code if needed (uncomment relevant lines)
# RUN make proto
# RUN make generate

# Build the application
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=${CGO_ENABLED} go build \
    -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o /bin/${PROJECT_NAME} ./cmd/server

# --- Final Stage ---
FROM alpine:3.23 AS final

WORKDIR /srv

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -S app && adduser -S app -G app

# Copy binary from builder
ARG PROJECT_NAME=server
COPY --from=builder /bin/${PROJECT_NAME} ./server

# Copy additional assets (customize as needed)
# COPY --from=builder /app/migrations ./migrations
# COPY --from=builder /app/assets ./assets

# Set ownership
RUN chown -R app:app /srv

# Switch to non-root user
USER app

# Expose port (customize as needed)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/healthz || exit 1

# Run the application
CMD ["./server"]

# =============================================================================
# Optional: Frontend Builder Stage (uncomment for full-stack apps)
# =============================================================================
# FROM node:22-alpine AS frontend-builder
#
# WORKDIR /app/frontend
#
# COPY frontend/package.json frontend/yarn.lock ./
# RUN yarn install --frozen-lockfile
#
# COPY frontend/ .
# RUN yarn build
#
# # Pre-compress assets with Brotli
# RUN apk add --no-cache brotli && \
#     find dist -type f \( -name "*.js" -o -name "*.css" -o -name "*.html" -o -name "*.svg" -o -name "*.json" \) \
#     -exec brotli -q 11 {} \;

# =============================================================================
# Optional: Nginx Final Stage (uncomment for full-stack apps)
# =============================================================================
# FROM alpine:3.23 AS final-nginx
#
# WORKDIR /srv
#
# RUN apk add --no-cache \
#     ca-certificates \
#     gettext-envsubst \
#     libcap \
#     nginx \
#     nginx-mod-http-brotli && \
#     mkdir -p /etc/nginx/conf.d
#
# COPY config/nginx.conf /etc/nginx/nginx.conf
# COPY config/default.conf.template /etc/nginx/conf.d/default.conf.template
# COPY --from=frontend-builder /app/frontend/dist /usr/share/nginx/html
# COPY --from=builder /bin/server ./server
# COPY start.sh /start.sh
# RUN chmod +x /start.sh
#
# RUN addgroup -S app && adduser -S app -G app && \
#     mkdir -p /etc/nginx/conf.d /srv /var/cache/nginx /var/lib/nginx /var/log/nginx /var/run && \
#     setcap cap_net_bind_service=+ep /usr/sbin/nginx && \
#     chown -R app:app /etc/nginx/conf.d /srv /start.sh /usr/share/nginx/html /var/cache/nginx /var/lib/nginx /var/log/nginx /var/run
#
# USER app
# EXPOSE 80 8080
# ENTRYPOINT ["/start.sh"]
