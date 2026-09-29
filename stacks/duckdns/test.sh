#!/bin/bash

set -euo pipefail

readonly source_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
temporary_directory=$(mktemp -d)
trap 'rm -rf -- "$temporary_directory"' EXIT HUP INT TERM

fake_curl=$temporary_directory/curl
install -m 0700 "$source_directory/fixtures/curl.sh" "$fake_curl"
export FAKE_DUCKDNS_RESPONSE FAKE_CURL_STATUS

run_core() {
	DUCKDNS_DOMAIN=mattikulasroom \
		DUCKDNS_TOKEN=fixture-token \
		"$source_directory/update.sh" --test-core "$temporary_directory/last_ip" "$fake_curl"
}

assert_equal() {
	local expected=$1
	local actual=$2
	local description=$3
	if [[ $actual != "$expected" ]]; then
		printf 'FAIL: %s\nexpected: %q\nactual:   %q\n' "$description" "$expected" "$actual" >&2
		exit 1
	fi
}

FAKE_DUCKDNS_RESPONSE=$'OK\n1.2.3.4\n\nNOCHANGE\n'
output=$(run_core)
assert_equal 'IPCHANGE: initialized public IP to 1.2.3.4' "$output" 'initialization event'
assert_equal '1.2.3.4' "$(<"$temporary_directory/last_ip")" 'initial state'

output=$(run_core)
assert_equal '' "$output" 'unchanged check is silent'

FAKE_DUCKDNS_RESPONSE=$'OK\n5.6.7.8\n\nUPDATED\n'
output=$(run_core)
assert_equal 'IPCHANGE: 1.2.3.4 -> 5.6.7.8' "$output" 'changed address event'
assert_equal '5.6.7.8' "$(<"$temporary_directory/last_ip")" 'changed state'

output=$(run_core)
assert_equal 'IPCHANGE: restored public IP to 5.6.7.8' "$output" 'remote record repair is logged'

FAKE_DUCKDNS_RESPONSE=$'KO\n'
if output=$(run_core 2>&1); then
	printf 'FAIL: KO response succeeded\n' >&2
	exit 1
fi
assert_equal "FAILURE: DuckDNS returned 'KO' while detecting public IP" "$output" 'KO response'
assert_equal '5.6.7.8' "$(<"$temporary_directory/last_ip")" 'failed update preserves state'

FAKE_DUCKDNS_RESPONSE=$'OK\n999.6.7.8\n\nUPDATED\n'
if output=$(run_core 2>&1); then
	printf 'FAIL: invalid IPv4 response succeeded\n' >&2
	exit 1
fi
assert_equal \
	"FAILURE: DuckDNS returned 'OK | 999.6.7.8 |  | UPDATED' while detecting public IP" \
	"$output" \
	'invalid IPv4 response'

FAKE_DUCKDNS_RESPONSE=
FAKE_CURL_STATUS=7
export FAKE_CURL_STATUS
if output=$(run_core 2>&1); then
	printf 'FAIL: curl failure succeeded\n' >&2
	exit 1
fi
assert_equal \
	'FAILURE: DuckDNS update request failed while detecting public IP' \
	"$output" \
	'curl failure'

FAKE_CURL_STATUS=0
for response in $'OK\n5.6.7.8\n\nUNKNOWN\n' $'OK\n5.6.7.8\n' $'OK\n5.6.7.8.\n\nUPDATED\n'; do
	FAKE_DUCKDNS_RESPONSE=$response
	if output=$(run_core 2>&1); then
		printf 'FAIL: malformed response succeeded\n' >&2
		exit 1
	fi
	assert_equal '5.6.7.8' "$(<"$temporary_directory/last_ip")" 'malformed response preserves state'
done

FAKE_DUCKDNS_RESPONSE=$'OK\r\n9.8.7.6\r\n\r\nNOCHANGE\r\n'
output=$(run_core)
assert_equal 'IPCHANGE: 5.6.7.8 -> 9.8.7.6' "$output" 'CRLF and remote NOCHANGE with new local IP'
assert_equal '600' "$(stat -c %a "$temporary_directory/last_ip")" 'state is private'

FAKE_DUCKDNS_RESPONSE='fixture-token'
if output=$(run_core 2>&1); then exit 1; fi
assert_equal "FAILURE: DuckDNS returned '[redacted]' while detecting public IP" "$output" 'response token is redacted'

if output=$(DUCKDNS_DOMAIN=mattikulasroom DUCKDNS_TOKEN= \
	"$source_directory/update.sh" --test-core "$temporary_directory/last_ip" "$fake_curl" 2>&1); then
	printf 'FAIL: empty token accepted\n' >&2
	exit 1
fi
assert_equal 'FAILURE: DuckDNS configuration is incomplete' "$output" 'empty token rejected'

FAKE_DUCKDNS_RESPONSE=$'OK\n1.1.1.1\n\nUPDATED\n'
mkdir "$temporary_directory/directory-state"
if output=$(DUCKDNS_DOMAIN=mattikulasroom DUCKDNS_TOKEN=fixture-token \
	"$source_directory/update.sh" --test-core "$temporary_directory/directory-state" "$fake_curl" 2>/dev/null); then
	printf 'FAIL: state write failure succeeded\n' >&2
	exit 1
fi
assert_equal 'FAILURE: could not publish DuckDNS state' "$output" 'state failure is reported without success'

printf 'DuckDNS updater tests passed\n'
