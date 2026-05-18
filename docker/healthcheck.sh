#!/bin/sh
set -eu

service="${1:-api}"

case "$service" in
  api)
    url="${MAILAPI_API_HEALTH_URL:-http://127.0.0.1:6060/healthz}"
    ;;
  smtp)
    url="${MAILAPI_SMTP_HEALTH_URL:-http://127.0.0.1:6061/healthz}"
    ;;
  worker)
    url="${MAILAPI_WORKER_HEALTH_URL:-http://127.0.0.1:6062/healthz}"
    ;;
  *)
    echo "unknown service: $service" >&2
    exit 2
    ;;
esac

wget -q -O /dev/null "$url"
