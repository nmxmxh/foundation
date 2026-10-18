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
export NGINX_CONFIG=${NGINX_CONFIG:-/etc/nginx/nginx.conf}

echo "Configuring nginx..."
echo "  Upstream: ${UPSTREAM_HOST}:${UPSTREAM_PORT}"

# Substitute environment variables in nginx config template
envsubst '${UPSTREAM_HOST} ${UPSTREAM_PORT}' \
    < /etc/nginx/conf.d/default.conf.template \
    > /etc/nginx/conf.d/default.conf

# Start nginx
echo "Starting nginx..."
if [ -f "/docker-entrypoint.sh" ]; then
    exec /docker-entrypoint.sh nginx -c "$NGINX_CONFIG" -g "daemon off;"
else
    exec nginx -c "$NGINX_CONFIG" -g "daemon off;"
fi
