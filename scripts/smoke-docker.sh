#!/usr/bin/env bash
set -euo pipefail
image="${1:-paperlens:test}"
docker build -t "$image" .
name="paperlens-smoke-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$name" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:noexec,uid=10001,gid=10001,mode=700 \
  --tmpfs /runtime:exec,uid=10001,gid=10001,mode=700 \
  --tmpfs /data:uid=10001,gid=10001,mode=700 \
  -e PAPERLENS_DEMO=true -p 127.0.0.1::8080 "$image" >/dev/null
test "$(docker exec "$name" id -u)" = 10001
docker exec "$name" /opt/copilot/copilot --version
docker exec "$name" paperlens runtime-check
docker exec "$name" paperlens health
port="$(docker port "$name" 8080/tcp | cut -d: -f2)"
curl --fail --silent --show-error -H 'Host: localhost:8080' "http://127.0.0.1:$port/readyz"
docker exec "$name" sh -c 'test -z "$(find /runtime/jobs -mindepth 1 -print -quit)"'
echo "non-root, read-only filesystem, readiness, restricted runtime and cleanup smoke passed"
