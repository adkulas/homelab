#!/usr/bin/env bash
# Shared lifecycle for disposable single-script Technitium rehearsals.
set -euo pipefail
case "${1:-}" in
  https_check.py|backup_check.py) ;;
  *) echo "Choose a supported rehearsal" >&2; exit 64 ;;
esac
cd "$(dirname "$0")/../../.."
umask 077
exec 9>/tmp/technitium-pilot-99.lock
flock -n 9 || { echo 'A Technitium pilot is already running' >&2; exit 1; }

rehearsal_workspace=$(mktemp -d /tmp/technitium-rehearsal.XXXXXX)
export PILOT_PASSWORD_FILE="$rehearsal_workspace/admin-password"
openssl rand -hex 24 > "$PILOT_PASSWORD_FILE"
compose=(docker compose --env-file services/network/technitium.env -f services/network/pilot/compose.yaml)
cleanup() {
  result=$?
  trap - EXIT
  if "${compose[@]}" down -v >/dev/null; then
    rm -rf "$rehearsal_workspace"
  else
    echo "Teardown failed; private workspace retained at $rehearsal_workspace" >&2
    echo "Retry: PILOT_PASSWORD_FILE=$PILOT_PASSWORD_FILE docker compose --env-file services/network/technitium.env -f services/network/pilot/compose.yaml down -v" >&2
    result=1
  fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
# Never adopt or delete an existing pilot's volumes, even when containers are stopped.
if [ -n "$(docker ps -aq --filter label=com.docker.compose.project=technitium-pilot)" ] ||
   [ -n "$(docker volume ls -q --filter label=com.docker.compose.project=technitium-pilot)" ]; then
  trap - EXIT
  rm -rf "$rehearsal_workspace"
  echo 'Existing pilot state found; clean it up explicitly before this rehearsal' >&2
  exit 1
fi
"${compose[@]}" config --quiet
"${compose[@]}" up -d >/dev/null
python3 -B "services/network/pilot/$1" "$rehearsal_workspace"
