FROM scratch AS go-base

# --- Go WASM Builder Stage ---
FROM go-base AS go-wasm-builder

ARG CACHE_NAMESPACE

WORKDIR /app

RUN --mount=type=cache,id=${CACHE_NAMESPACE}-apk-gowasm,target=/var/cache/apk,sharing=locked \
    apk add --update-cache binaryen brotli

COPY go.mod go.sum ./
COPY wasm/ ./wasm/

RUN --mount=type=cache,id=${CACHE_NAMESPACE}-gomod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=${CACHE_NAMESPACE}-gobuild,target=/root/.cache/go-build,sharing=locked \
    mkdir -p /out && \
    GOROOT=$(go env GOROOT) && \
    if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then cp "$GOROOT/lib/wasm/wasm_exec.js" /out/wasm_exec.js; \
    elif [ -f "$GOROOT/misc/wasm/wasm_exec.js" ]; then cp "$GOROOT/misc/wasm/wasm_exec.js" /out/wasm_exec.js; else \
    echo "Error: wasm_exec.js not found in GOROOT ($GOROOT)." >&2; exit 1; fi && \
    GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o /out/main.wasm ./wasm && \
    (wasm-opt -O1 --strip-debug --enable-bulk-memory --enable-mutable-globals --enable-sign-ext --enable-nontrapping-float-to-int -o /out/main.wasm.opt /out/main.wasm && mv /out/main.wasm.opt /out/main.wasm || rm -f /out/main.wasm.opt) && \
    brotli -q 11 -f -k /out/main.wasm

# --- Rust WASM Builder Stage ---
FROM scratch AS rust-wasm-builder
COPY rust/ /rust/

# --- Frontend Builder ---
FROM scratch AS frontend-builder
COPY frontend/ ./frontend/

# Copy Go WASM artifacts
COPY --from=go-wasm-builder /out/main.wasm* ./frontend/public/
COPY --from=go-wasm-builder /out/wasm_exec.js ./frontend/public/

# Copy Rust WASM modules
COPY --from=rust-wasm-builder /out/modules/ ./frontend/public/modules/

FROM scratch AS frontend
COPY --from=frontend-builder /app/frontend/dist /usr/share/nginx/html
