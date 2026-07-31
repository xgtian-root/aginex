#!/usr/bin/env bash

set -euo pipefail

target="${1:-}"
image_ref="${2:-}"
ops_image_ref="${3:-$image_ref}"

case "$target" in
  api|worker|migrate|web) ;;
  *)
    echo "unsupported image target: $target" >&2
    exit 2
    ;;
esac
if [[ -z "$image_ref" || -z "$ops_image_ref" ]]; then
  echo "image references are required" >&2
  exit 2
fi

run_marker="${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-${target}"
runtime_container="aginex-smoke-${run_marker}"
postgres_container="aginex-smoke-postgres-${run_marker}"
runtime_volume="aginex-smoke-data-${run_marker}"
runtime_network="aginex-smoke-net-${run_marker}"
postgres_image="postgres:18-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"

cleanup() {
  docker rm --force "$runtime_container" >/dev/null 2>&1 || :
  docker rm --force "$postgres_container" >/dev/null 2>&1 || :
  docker volume rm "$runtime_volume" >/dev/null 2>&1 || :
  docker network rm "$runtime_network" >/dev/null 2>&1 || :
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
  --env=AGINEX_API_PUBLIC_URL=http://127.0.0.1:8080
  --env=AGINEX_WEB_ORIGINS=http://127.0.0.1:3000
  --env=AGINEX_SESSION_SECRET=ci-runtime-smoke-secret-not-for-production
  --env=AGINEX_SESSION_SECURE=false
  --env=AGINEX_IDEMPOTENCY_DRIVER=database
)

wait_for_url() {
  local url="$1"
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if curl --fail --silent --show-error "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$runtime_container")" != "true" ]]; then
      docker logs "$runtime_container" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "$runtime_container" >&2
  echo "timed out waiting for $url" >&2
  return 1
}

stop_and_require_clean_exit() {
  docker stop --time 20 "$runtime_container" >/dev/null
  local exit_code
  exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$runtime_container")"
  if [[ "$exit_code" != "0" ]]; then
    docker logs "$runtime_container" >&2
    echo "$target exited with status $exit_code after SIGTERM" >&2
    return 1
  fi
}

prepare_sqlite() {
  docker volume create "$runtime_volume" >/dev/null
  docker run --rm \
    "${common_runtime_options[@]}" \
    --mount="type=volume,source=$runtime_volume,target=/data" \
    "${common_aginex_environment[@]}" \
    --env=AGINEX_DATABASE_DRIVER=sqlite \
    --env=AGINEX_DATABASE_DSN=/data/aginex.db \
    --env=AGINEX_STORAGE_DRIVER=local \
    --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
    --env=AGINEX_JOBS_DRIVER=disabled \
    "$ops_image_ref" migrate up
}

case "$target" in
  api)
    prepare_sqlite
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      --publish=127.0.0.1::8080 \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_DATABASE_DRIVER=sqlite \
      --env=AGINEX_DATABASE_DSN=/data/aginex.db \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=disabled \
      "$image_ref" >/dev/null
    api_address="$(docker port "$runtime_container" 8080/tcp)"
    wait_for_url "http://${api_address}/health/ready"
    stop_and_require_clean_exit
    ;;

  worker)
    docker network create "$runtime_network" >/dev/null
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
    docker run --rm \
      "${common_runtime_options[@]}" \
      --network="$runtime_network" \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_DATABASE_DRIVER=postgres \
      --env="AGINEX_DATABASE_DSN=$postgres_dsn" \
      --env=AGINEX_JOBS_DRIVER=postgres \
      "$ops_image_ref" migrate up
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --network="$runtime_network" \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_DATABASE_DRIVER=postgres \
      --env="AGINEX_DATABASE_DSN=$postgres_dsn" \
      --env=AGINEX_JOBS_DRIVER=postgres \
      --env=AGINEX_JOBS_WORKER_ID=ci-runtime-smoke \
      "$image_ref" >/dev/null
    sleep 2
    if [[ "$(docker inspect --format '{{.State.Running}}' "$runtime_container")" != "true" ]]; then
      docker logs "$runtime_container" >&2
      exit 1
    fi
    stop_and_require_clean_exit
    ;;

  migrate)
    prepare_sqlite
    docker run --rm \
      "${common_runtime_options[@]}" \
      --mount="type=volume,source=$runtime_volume,target=/data" \
      "${common_aginex_environment[@]}" \
      --env=AGINEX_DATABASE_DRIVER=sqlite \
      --env=AGINEX_DATABASE_DSN=/data/aginex.db \
      --env=AGINEX_STORAGE_DRIVER=local \
      --env=AGINEX_STORAGE_LOCAL_ROOT=/data/uploads \
      --env=AGINEX_JOBS_DRIVER=disabled \
      "$image_ref" migrate status
    ;;

  web)
    docker run --detach \
      --name="$runtime_container" \
      "${common_runtime_options[@]}" \
      --tmpfs=/app/apps/web/.next/cache:rw,nosuid,nodev,size=128m \
      --publish=127.0.0.1::3000 \
      "$image_ref" >/dev/null
    web_address="$(docker port "$runtime_container" 3000/tcp)"
    wait_for_url "http://${web_address}/login"
    stop_and_require_clean_exit
    ;;
esac
