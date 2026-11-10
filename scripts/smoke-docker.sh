#!/usr/bin/env bash
set -euo pipefail
image="${1:-paperlens:test}"
case "${2:-build}" in
  build) docker build -t "$image" . ;;
  --pull) docker pull "$image" ;;
  --loaded) docker image inspect "$image" >/dev/null ;;
  *) echo "Usage: $0 [image] [--pull|--loaded]" >&2; exit 2 ;;
esac
name="paperlens-smoke-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$name" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:noexec,uid=10001,gid=10001,mode=700 \
  --tmpfs /runtime:exec,uid=10001,gid=10001,mode=700 \
  --tmpfs /data:uid=10001,gid=10001,mode=700 \
  -e PAPERLENS_DEMO=true -p 127.0.0.1::8080 "$image" >/dev/null
ready=false
for ((attempt=0; attempt<30; attempt++)); do
  if docker exec "$name" paperlens health >/dev/null 2>&1; then
    ready=true
    break
  fi
  if test "$(docker inspect --format '{{.State.Running}}' "$name")" != true; then
    docker logs "$name"
    exit 1
  fi
  sleep 1
done
if test "$ready" != true; then
  docker logs "$name"
  echo "Container did not become ready" >&2
  exit 1
fi
test "$(docker exec "$name" id -u)" = 10001
cli_version="$(docker exec "$name" /opt/copilot/copilot --version)"
printf '%s\n' "$cli_version"
grep -Fq 'GitHub Copilot CLI 1.0.105.' <<< "$cli_version"
docker exec "$name" paperlens runtime-check
docker exec "$name" paperlens health
port="$(docker port "$name" 8080/tcp | cut -d: -f2)"
curl --fail --silent --show-error -H 'Host: localhost:8080' "http://127.0.0.1:$port/readyz"
docker exec "$name" sh -c 'test -z "$(find /runtime/jobs -mindepth 1 -print -quit)"'
docker stop --time 45 "$name" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$name")" = 0
echo "non-root, read-only filesystem, readiness, restricted runtime and cleanup smoke passed"
