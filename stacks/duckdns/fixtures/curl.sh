#!/bin/bash
set -euo pipefail
token=$(cat)
[[ $token == fixture-token ]] || exit 90
[[ $* != *fixture-token* ]] || exit 91
[[ $1 == --disable && $2 == --ipv4 ]] || exit 92
printf '%b' "${FAKE_DUCKDNS_RESPONSE:-}"
exit "${FAKE_CURL_STATUS:-0}"
