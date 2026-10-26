#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../../.."
compose=(docker compose --env-file services/network/technitium.env -f services/network/pilot/compose.yaml)
"${compose[@]}" config --quiet

query_resolver() {
  local service=$1 domain=$2 id=$3 address response
  address=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$id")
  if [[ -z "$address" ]]; then
    echo "$service: no pilot bridge address" >&2
    return 1
  fi
  for attempt in 1 2 3; do
    if response=$(dig +time=15 +tries=1 +noall +comments +answer @"$address" "$domain" A) &&
      [[ "$response" == *"status: NOERROR"* && "$response" == *"$domain."* ]]; then
      return 0
    fi
    sleep 5
  done
  echo "$service: recursive query failed" >&2
  return 1
}

for service in primary secondary; do
  id=$("${compose[@]}" ps -q "$service")
  if [[ -z "$id" || "$(docker inspect -f '{{.State.Running}}' "$id")" != true ]]; then
    echo "$service: not running" >&2
    exit 1
  fi
  if [[ -n "$(docker port "$id")" ]]; then
    echo "$service: published Host port found" >&2
    exit 1
  fi
  network_name=$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$id")
  if [[ "$network_name" != technitium-pilot_pilot ]]; then
    echo "$service: expected one isolated pilot network" >&2
    exit 1
  fi
  query_resolver "$service" example.com "$id"
  echo "$service: running; no published ports; recursive query answered"
done

trap '"${compose[@]}" start primary secondary >/dev/null 2>&1' EXIT
for service in primary secondary; do
  if [[ "$service" == primary ]]; then peer=secondary; else peer=primary; fi
  "${compose[@]}" stop "$peer" >/dev/null
  "${compose[@]}" start "$service" >/dev/null
  id=$("${compose[@]}" ps -q "$service")
  query_resolver "$service" iana.org "$id"
  echo "$service: recursive query answered while peer stopped"
done
