#!/usr/bin/env bash
# One disposable Docker pilot at a time; always remove its credentials and volumes.
set -euo pipefail
cd "$(dirname "$0")/../../.."
umask 077
exec 9>/tmp/technitium-pilot-99.lock
if ! flock -n 9; then
  echo 'A Technitium pilot is already running' >&2
  exit 1
fi

pilot_password_file=$(mktemp /tmp/technitium-pilot-password.XXXXXX)
openssl rand -hex 24 | tr -d '\n' > "$pilot_password_file"
export PILOT_PASSWORD_FILE="$pilot_password_file"
compose=(docker compose --env-file services/network/technitium.env -f services/network/pilot/compose.yaml)
cleanup() {
  result=$?
  trap - EXIT
  if "${compose[@]}" down -v >/dev/null; then
    rm -f "$pilot_password_file"
  else
    echo "Pilot teardown failed; private password file retained at $pilot_password_file" >&2
    echo "Retry cleanup: PILOT_PASSWORD_FILE=$pilot_password_file docker compose --env-file services/network/technitium.env -f services/network/pilot/compose.yaml down -v" >&2
    result=1
  fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

"${compose[@]}" config --quiet
"${compose[@]}" up -d >/dev/null
services/network/pilot/check.sh
services/network/pilot/cluster_check.py
services/network/pilot/dhcp_check.py
"${compose[@]}" down -v >/dev/null
"${compose[@]}" up -d >/dev/null
services/network/pilot/automation_check.py
