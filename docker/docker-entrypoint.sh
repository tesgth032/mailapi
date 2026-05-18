#!/bin/sh
set -eu

config="${MAILAPI_CONFIG:-/etc/mailapi/config.yaml}"

has_config_arg() {
  for arg in "$@"; do
    case "$arg" in
      --config|--config=*)
        return 0
        ;;
    esac
  done
  return 1
}

run_service() {
  service="$1"
  shift || true

  bin="/usr/local/bin/mailapi-${service}"
  if ! has_config_arg "$@"; then
    exec "$bin" --config "$config" "$@"
  fi
  exec "$bin" "$@"
}

case "${1:-api}" in
  api|smtp|worker)
    service="$1"
    shift || true
    run_service "$service" "$@"
    ;;
  mailapi-api)
    shift || true
    run_service api "$@"
    ;;
  mailapi-smtp)
    shift || true
    run_service smtp "$@"
    ;;
  mailapi-worker)
    shift || true
    run_service worker "$@"
    ;;
  *)
    exec "$@"
    ;;
esac
