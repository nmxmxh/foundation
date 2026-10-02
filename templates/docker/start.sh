#!/bin/sh
set -e

# =============================================================================
# Container Startup Script
# =============================================================================
# This script handles:
# 1. Optional backend server startup (for single-container deployments)
# 2. Nginx configuration template substitution
# 3. Starting nginx in foreground mode
# =============================================================================

# Start backend server if requested (for single-container mode)
if [ "${RUN_BACKEND_IN_CONTAINER:-false}" = "true" ]; then
    echo "Starting backend server..."
    ./server &
    # Wait for backend to be ready
    sleep 2
fi

# Set default upstream values
export UPSTREAM_HOST=${UPSTREAM_HOST:-server}
export UPSTREAM_PORT=${UPSTREAM_PORT:-8080}
# Upstream keepalive sizing. Each idle socket costs a file descriptor in both
# nginx and the Go backend, so the pool is bounded and overridable per deploy.
export UPSTREAM_KEEPALIVE_CONNECTIONS=${UPSTREAM_KEEPALIVE_CONNECTIONS:-64}
export UPSTREAM_KEEPALIVE_REQUESTS=${UPSTREAM_KEEPALIVE_REQUESTS:-1000}
export UPSTREAM_KEEPALIVE_TIMEOUT=${UPSTREAM_KEEPALIVE_TIMEOUT:-60s}
export NGINX_CONFIG=${NGINX_CONFIG:-/etc/nginx/nginx.conf}

echo "Configuring nginx..."
echo "  Upstream: ${UPSTREAM_HOST}:${UPSTREAM_PORT}"
echo "  Keepalive pool: ${UPSTREAM_KEEPALIVE_CONNECTIONS} connections, ${UPSTREAM_KEEPALIVE_TIMEOUT} idle"

# Substitute environment variables in nginx config template.
# The variable list is explicit: nginx configuration uses bare $name for its own
# variables, and a bare envsubst would replace those with empty strings.
envsubst '${UPSTREAM_HOST} ${UPSTREAM_PORT} ${UPSTREAM_KEEPALIVE_CONNECTIONS} ${UPSTREAM_KEEPALIVE_REQUESTS} ${UPSTREAM_KEEPALIVE_TIMEOUT}' \
    < /etc/nginx/conf.d/default.conf.template \
    > /etc/nginx/conf.d/default.conf

# Verify the rendered config before starting. A bad template then fails here
# with a readable message instead of at container start.
nginx -t -c "$NGINX_CONFIG"

# Start nginx
echo "Starting nginx..."
if [ -f "/docker-entrypoint.sh" ]; then
    exec /docker-entrypoint.sh nginx -c "$NGINX_CONFIG" -g "daemon off;"
else
    exec nginx -c "$NGINX_CONFIG" -g "daemon off;"
fi
