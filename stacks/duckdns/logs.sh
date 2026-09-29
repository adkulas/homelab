#!/bin/bash

set -euo pipefail

if [[ $# -gt 1 ]]; then
	printf 'usage: duckdns-logs [SINCE]\n' >&2
	exit 64
fi

since=${1:-30 days ago}
/usr/bin/journalctl \
	--unit=duckdns.service \
	--since="$since" \
	--no-pager \
	--quiet \
	--output=cat | /usr/bin/awk '/^(FAILURE|IPCHANGE):/'
