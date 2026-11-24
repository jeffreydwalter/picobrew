#!/usr/bin/env bash
# Exercise the actual Compose service on an isolated volume with no USB device attached.
set -euo pipefail
cd "$(dirname "$0")/.."
image=${1:?expected a local image}
platform=${2:?expected a Linux platform}
export HUB_TEST_IMAGE="$image" HUB_TEST_PLATFORM="$platform"
scratch=$(mktemp -d)
project="hub-ci-$$"
name="$project-hub"
compose=(docker compose --project-name "$project" -f compose.yaml -f "$scratch/test.yaml")
cat > "$scratch/test.yaml" <<'YAML'
services:
  hub:
    image: ${HUB_TEST_IMAGE}
    platform: ${HUB_TEST_PLATFORM}
YAML

cleanup() {
  if [ "$?" != 0 ]; then docker logs --tail 30 "$name" >&2 || true; fi
  docker rm -f "$name" >/dev/null 2>&1 || true
  "${compose[@]}" down -v >/dev/null
  rm -rf "$scratch"
}
trap cleanup EXIT
"${compose[@]}" run -d --no-deps --pull never --name "$name" -p 127.0.0.1::8080 \
  -e HUB_PASSWORD=isolated-container-smoke-password hub
health() {
  url="http://$(docker port "$name" 8080/tcp)"
  curl --fail --silent --show-error --retry 40 --retry-delay 1 --retry-all-errors --max-time 5 "$url/healthz"
}
health
[[ "$(curl -fsS "$url/")" == *'<title>PicoBrew Hub</title>'* ]]
curl -fsS "$url/third-party-licenses.md" --output /dev/null
curl -fsS -c "$scratch/cookies" --json '{"password":"isolated-container-smoke-password"}' \
  "$url/api/v1/auth/login" >/dev/null
# Enumeration materializes the worker embedded in the running Hub. No hardware
# is passed through; the worker still has to unpack, start and complete.
curl -fsS -b "$scratch/cookies" "$url/api/v1/usb/devices" | jq -e '.available and (.devices | type == "array")'
docker exec "$name" sh -ec 'set -- /data/usb-worker/worker-*; test "$#" -eq 1; exec "$1" --self-test' | jq -e '.type == "complete"'
docker restart "$name" >/dev/null
health
docker exec "$name" sh -ec 'set -- /data/usb-worker/worker-*; test "$#" -eq 1 && test -x "$1"'
