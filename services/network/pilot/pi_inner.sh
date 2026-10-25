#!/usr/bin/env bash
set -euo pipefail

ip link set lo up
while [[ ! -f "$PILOT_DIR/net-ready" ]]; do sleep 0.2; done
export DOTNET_ROOT="$PILOT_DIR/dotnet"
export DNS_SERVER_DOMAIN=pilot-pi.test
export DNS_SERVER_ADMIN_PASSWORD_FILE="$PILOT_DIR/admin-password"
export DNS_SERVER_WEB_SERVICE_LOCAL_ADDRESSES=127.0.0.1
export DNS_SERVER_WEB_SERVICE_HTTP_PORT=15380
export DNS_SERVER_RECURSION=AllowOnlyForPrivateNetworks
export DNS_SERVER_ENABLE_BLOCKING=true
mkdir -p "$PILOT_DIR/config"
"$DOTNET_ROOT/dotnet" "$PILOT_DIR/app/DnsServerApp.dll" \
  --portable-app --stop-if-bind-fails "$PILOT_DIR/config" > "$PILOT_DIR/native.log" 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true' EXIT
ready=no
for attempt in $(seq 1 30); do
  if curl -fsS --max-time 2 http://127.0.0.1:15380/api/dnsClient/healthCheck 2>/dev/null |
      grep -q '"status":"ok"'; then
    ready=yes
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then break; fi
  sleep 2
done
if [[ "$ready" != yes ]]; then
  echo 'Native Pi pilot startup failed' >&2
  exit 1
fi
export PILOT_SERVER_PID="$server_pid"
python3 "$PILOT_PROBE"
