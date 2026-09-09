#!/usr/bin/env bash
set -euo pipefail

cli_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export GOWORK=off
export GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" GOFLAGS=""
exec go -C "$cli_dir" run -mod=readonly ./cmd/release build --root "$cli_dir/.." "$@"
