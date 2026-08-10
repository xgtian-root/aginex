#!/usr/bin/env bash

set -euo pipefail

target="${1:-}"
image_ref="${2:-}"
api_image_ref="${3:-}"

case "$target" in
  api|worker|web) ;;
  *)
    echo "unsupported image target: $target" >&2
    exit 2
    ;;
esac
if [[ -z "$image_ref" ]]; then
  echo "an image reference is required" >&2
  exit 2
fi
if [[ "$target" == "worker" && -z "$api_image_ref" ]]; then
  echo "the worker smoke test requires an API image reference" >&2
  exit 2
fi

run_marker="${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-${target}"
runtime_container="aginex-smoke-${run_marker}"
api_container="aginex-smoke-api-${run_marker}"
postgres_container="aginex-smoke-postgres-${run_marker}"
runtime_volume="aginex-smoke-data-${run_marker}"
runtime_network="aginex-smoke-net-${run_marker}"
postgres_image="postgres:18-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
csrf_cookie_jar=""

cleanup() {
  docker rm --force "$runtime_container" >/dev/null 2>&1 || :
  docker rm --force "$api_container" >/dev/null 2>&1 || :
  docker rm --force "$postgres_container" >/dev/null 2>&1 || :
  docker volume rm "$runtime_volume" >/dev/null 2>&1 || :
  docker network rm "$runtime_network" >/dev/null 2>&1 || :
  if [[ -n "$csrf_cookie_jar" ]]; then
    rm -f -- "$csrf_cookie_jar"
  fi
}
trap cleanup EXIT
cleanup

common_runtime_options=(
  --read-only
  --cap-drop=ALL
  --security-opt=no-new-privileges=true
  --tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m
)
common_aginex_environment=(
  --env=AGINEX_ENV=test
  --env=AGINEX_CONFIG_FILE=/data/aginex-config.json
  --env=AGINEX_API_PUBLIC_URL=http://127.0.0.1:8080
  --env=AGINEX_WEB_ORIGINS=http://127.0.0.1:3000
  --env=AGINEX_SESSION_SECURE=false
  --env=AGINEX_IDEMPOTENCY_DRIVER=database
)
environment_installation_credentials=(
  --env=AGINEX_SESSION_SECRET=ci-runtime-smoke-secret-not-for-production
  --env=AGINEX_BOOTSTRAP_ADMIN_EMAIL=ci-admin@example.com
  --env=AGINEX_BOOTSTRAP_ADMIN_PASSWORD=correct-ci-runtime-smoke-password
)

wait_for_url() {
  local container="$1"
  local url="$2"
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if curl --fail --silent --show-error "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" != "true" ]]; then
      docker logs "$container" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "$container" >&2
  echo "timed out waiting for $url" >&2
  return 1
}

require_mode() {
  local container="$1"
  local url="$2"
  local expected="$3"
  local response
  response="$(curl --fail --silent --show-error "$url")"
  if [[ "$response" != "{\"mode\":\"${expected}\"}" ]]; then
    docker logs "$container" >&2
    echo "API did not enter $expected mode: $response" >&2
    return 1
  fi
}

wait_for_application_mode() {
  local container="$1"
  local url="$2"
  local response
  for ((attempt = 1; attempt <= 120; attempt++)); do
    if response="$(curl --fail --silent --show-error "$url" 2>/dev/null)" &&
      [[ "$response" == '{"mode":"application"}' ]]; then
      return 0
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" != "true" ]]; then
      docker logs "$container" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "$container" >&2
  echo "timed out waiting for application mode" >&2
  return 1
}

wait_for_worker_started() {
  local container="$1"
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if docker logs "$container" 2>&1 |
      grep -Fq 'Aginex worker started'; then
      return 0
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" != "true" ]]; then
      docker logs "$container" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "$container" >&2
  echo "timed out waiting for worker startup" >&2
  return 1
}

require_setup_closed() {
  local container="$1"
  local url="$2"
  local response
  local status
  local headers
  response="$(
    curl --silent --show-error \
      --dump-header - \
      --output /dev/null \
      --write-out $'\n%{http_code}' \
      "$url/api/v1/setup/status"
  )"
  status="${response##*$'\n'}"
  headers="${response%$'\n'*}"
  if [[ "$status" != "404" ]] ||
    ! grep -Eiq '^cache-control:[[:space:]]*.*no-store' <<<"$headers"; then
    docker logs "$container" >&2
    echo "Setup API was not permanently closed (status $status)" >&2
    return 1
  fi
}

stop_and_require_clean_exit() {
  local container="$1"
  local label="$2"
  docker stop --time 20 "$container" >/dev/null
  local exit_code
  exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$container")"
  if [[ "$exit_code" != "0" ]]; then
    docker logs "$container" >&2
    echo "$label exited with status $exit_code after SIGTERM" >&2
    return 1
  fi
}

case "$target" in
  api)
    docker volume create "$runtime_volume" >/dev/null
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      --publish=127.0.0.1::8080 \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=disabled \
      "$image_ref" >/dev/null
    api_address="$(docker port "$runtime_container" 8080/tcp)"
    wait_for_url "$runtime_container" "http://${api_address}/health/ready"
    api_url="http://${api_address}"
    require_mode "$runtime_container" "$api_url/api/v1/system/mode" setup

    setup_status="$(
      curl --fail --silent --show-error "$api_url/api/v1/setup/status"
    )"
    if [[ "$setup_status" != '{"status":"required","stage":"waiting"}' ]]; then
      docker logs "$runtime_container" >&2
      echo "API did not expose the required Setup state" >&2
      exit 1
    fi

    csrf_cookie_jar="$(mktemp "${TMPDIR:-/tmp}/aginex-smoke-cookies.XXXXXX")"
    csrf_response="$(
      curl --fail --silent --show-error \
        --cookie-jar "$csrf_cookie_jar" \
        --header 'Origin: http://127.0.0.1:3000' \
        "$api_url/api/v1/auth/csrf"
    )"
    csrf_pattern='"token":"([A-Za-z0-9_-]{43})"'
    if [[ "$csrf_response" != *'"headerName":"X-CSRF-Token"'* ]] ||
      [[ ! "$csrf_response" =~ $csrf_pattern ]]; then
      docker logs "$runtime_container" >&2
      echo "API did not issue the expected Setup CSRF protocol" >&2
      exit 1
    fi
    csrf_token="${BASH_REMATCH[1]}"

    database_request='{"database":{"driver":"sqlite","sqlite":{"directory":"/data","filename":"aginex.db"}}}'
    database_response="$(
      curl --fail --silent --show-error \
        --cookie "$csrf_cookie_jar" \
        --header 'Origin: http://127.0.0.1:3000' \
        --header 'Content-Type: application/json' \
        --header "X-CSRF-Token: $csrf_token" \
        --data-binary "$database_request" \
        "$api_url/api/v1/setup/database/test"
    )"
    if [[ "$database_response" != '{"status":"ok"}' ]]; then
      docker logs "$runtime_container" >&2
      echo "Setup database verification did not succeed" >&2
      exit 1
    fi

    complete_request='{"database":{"driver":"sqlite","sqlite":{"directory":"/data","filename":"aginex.db"}},"administrator":{"email":"ci-admin@example.com","password":"correct-ci-runtime-smoke-password"}}'
    complete_response="$(
      curl --silent --show-error \
        --cookie "$csrf_cookie_jar" \
        --header 'Origin: http://127.0.0.1:3000' \
        --header 'Content-Type: application/json' \
        --header "X-CSRF-Token: $csrf_token" \
        --data-binary "$complete_request" \
        --write-out ' %{http_code}' \
        "$api_url/api/v1/setup/complete"
    )"
    if [[ "$complete_response" != '{"status":"initializing"} 202' ]]; then
      docker logs "$runtime_container" >&2
      echo "Setup completion was not accepted" >&2
      exit 1
    fi

    wait_for_application_mode "$runtime_container" "$api_url/api/v1/system/mode"
    wait_for_url "$runtime_container" "$api_url/health/ready"
    require_setup_closed "$runtime_container" "$api_url"
    stop_and_require_clean_exit "$runtime_container" api

    docker rm "$runtime_container" >/dev/null
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      --publish=127.0.0.1::8080 \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=disabled \
      "$image_ref" >/dev/null
    api_address="$(docker port "$runtime_container" 8080/tcp)"
    api_url="http://${api_address}"
    wait_for_url "$runtime_container" "$api_url/health/ready"
    require_mode "$runtime_container" "$api_url/api/v1/system/mode" application
    require_setup_closed "$runtime_container" "$api_url"
    stop_and_require_clean_exit "$runtime_container" "restarted API"
    rm -f -- "$csrf_cookie_jar"
    csrf_cookie_jar=""
    ;;

  worker)
    docker network create "$runtime_network" >/dev/null
    docker volume create "$runtime_volume" >/dev/null
    docker run --detach \
      --name="$postgres_container" \
      --network="$runtime_network" \
      --env=POSTGRES_DB=aginex \
      --env=POSTGRES_USER=aginex \
      --env=POSTGRES_PASSWORD=aginex \
      "$postgres_image" >/dev/null
    for ((attempt = 1; attempt <= 60; attempt++)); do
      if docker exec "$postgres_container" pg_isready -U aginex -d aginex >/dev/null 2>&1; then
        break
      fi
      if [[ "$attempt" == "60" ]]; then
        docker logs "$postgres_container" >&2
        echo "timed out waiting for PostgreSQL" >&2
        exit 1
      fi
      sleep 1
    done
    postgres_dsn="postgres://aginex:aginex@${postgres_container}:5432/aginex?sslmode=disable"
    docker run --detach \
      --name="$api_container" \
      "${common_runtime_options[@]}" \
      --network="$runtime_network" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      --publish=127.0.0.1::8080 \
      "${common_aginex_environment[@]}" \
      "${environment_installation_credentials[@]}" \
      --env=AGINEX_DATABASE_DRIVER=postgres \
      --env="AGINEX_DATABASE_DSN=$postgres_dsn" \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=postgres \
      "$api_image_ref" >/dev/null
    api_address="$(docker port "$api_container" 8080/tcp)"
    wait_for_url "$api_container" "http://${api_address}/health/ready"
    require_mode "$api_container" "http://${api_address}/api/v1/system/mode" application

    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --network="$runtime_network" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      "${common_aginex_environment[@]}" \
      "${environment_installation_credentials[@]}" \
      --env="AGINEX_API_PUBLIC_URL=http://${api_container}:8080" \
      --env=AGINEX_DATABASE_DRIVER=postgres \
      --env="AGINEX_DATABASE_DSN=$postgres_dsn" \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=postgres \
      --env=AGINEX_JOBS_WORKER_ID=ci-runtime-smoke \
      "$image_ref" >/dev/null
    wait_for_worker_started "$runtime_container"
    stop_and_require_clean_exit "$runtime_container" worker
    ;;

  web)
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --tmpfs=/app/apps/web/.next/cache:rw,nosuid,nodev,size=128m \
      --publish=127.0.0.1::3000 \
      "$image_ref" >/dev/null
    web_address="$(docker port "$runtime_container" 3000/tcp)"
    wait_for_url "$runtime_container" "http://${web_address}/login"
    stop_and_require_clean_exit "$runtime_container" web
    ;;
esac
