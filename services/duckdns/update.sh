#!/bin/bash

set -euo pipefail

readonly config_file=/etc/duckdns/secrets.sops.yaml
readonly state_file=/var/lib/duckdns/last_ip
readonly sops_bin=/usr/local/bin/sops
readonly curl_bin=/usr/bin/curl
readonly update_url=https://www.duckdns.org/update
readonly logged_failure_status=70

failure() {
	# Keep application events on stdout; suppress raw SOPS diagnostics on stderr.
	printf 'FAILURE: %s\n' "$*"
}

valid_ipv4() {
	local ip=$1
	local octet
	local -a octets

	[[ $ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
	IFS=. read -r -a octets <<<"$ip"
	[[ ${#octets[@]} -eq 4 ]] || return 1
	for octet in "${octets[@]}"; do
		[[ $octet =~ ^[0-9]{1,3}$ ]] || return 1
		((10#$octet <= 255)) || return 1
	done
}

response_summary() {
	local response=$1
	response=${response//"${DUCKDNS_TOKEN}"/[redacted]}
	response=${response//$'\n'/ | }
	response=${response//$'\r'/}
	response=${response//$'\t'/ }
	response=$(LC_ALL=C /usr/bin/printf '%s' "$response" | /usr/bin/tr -cd '[:print:]')
	printf '%.200s' "$response"
}

save_state() {
	local ip=$1
	local destination=$2
	local temporary="${destination}.tmp.$$"

	umask 077
	if ! printf '%s\n' "$ip" >"$temporary"; then
		failure "could not write DuckDNS state"
		return "$logged_failure_status"
	fi
	if ! /usr/bin/mv -fT -- "$temporary" "$destination"; then
		/usr/bin/rm -f -- "$temporary"
		failure "could not publish DuckDNS state"
		return "$logged_failure_status"
	fi
}

run_update() {
	local destination=$1
	local curl_command=$2
	local response
	local summary
	local previous_ip=
	local -a lines

	if [[ -z ${DUCKDNS_DOMAIN:-} || -z ${DUCKDNS_TOKEN:-} ]]; then
		failure "DuckDNS configuration is incomplete"
		return "$logged_failure_status"
	fi
	if [[ ! $DUCKDNS_DOMAIN =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
		failure "DuckDNS domain is invalid"
		return "$logged_failure_status"
	fi

	# Send the token through stdin so it never appears in curl's argument list.
	if ! response=$(
		printf '%s' "$DUCKDNS_TOKEN" |
			"$curl_command" \
				--disable \
				--ipv4 \
				--silent \
				--fail-with-body \
				--connect-timeout 10 \
				--max-time 30 \
				--get \
				--data-urlencode "domains=$DUCKDNS_DOMAIN" \
				--data-urlencode 'token@-' \
				--data-urlencode 'ip=' \
				--data-urlencode 'verbose=true' \
				"$update_url" |
			/usr/bin/tr -d '\r'
	); then
		failure "DuckDNS update request failed while detecting public IP"
		return "$logged_failure_status"
	fi

	mapfile -t lines <<<"$response"
	if [[ ${#lines[@]} -ne 4 || ${lines[0]} != OK ]]; then
		summary=$(response_summary "$response")
		failure "DuckDNS returned '$summary' while detecting public IP"
		return "$logged_failure_status"
	fi
	if ! valid_ipv4 "${lines[1]}"; then
		summary=$(response_summary "$response")
		failure "DuckDNS returned '$summary' while detecting public IP"
		return "$logged_failure_status"
	fi
	if [[ ${lines[3]} != UPDATED && ${lines[3]} != NOCHANGE ]]; then
		summary=$(response_summary "$response")
		failure "DuckDNS returned '$summary' for IP ${lines[1]}"
		return "$logged_failure_status"
	fi

	if [[ -f $destination ]]; then
		if ! IFS= read -r previous_ip <"$destination"; then
			failure "could not read DuckDNS state"
			return "$logged_failure_status"
		fi
		if [[ -n $previous_ip ]] && ! valid_ipv4 "$previous_ip"; then
			failure "stored DuckDNS state is invalid"
			return "$logged_failure_status"
		fi
	fi

	if [[ $previous_ip == "${lines[1]}" ]]; then
		if [[ ${lines[3]} == UPDATED ]]; then
			printf 'IPCHANGE: restored public IP to %s\n' "$previous_ip"
		fi
		return 0
	fi

	save_state "${lines[1]}" "$destination" || return $?
	if [[ -z $previous_ip ]]; then
		printf 'IPCHANGE: initialized public IP to %s\n' "${lines[1]}"
	else
		printf 'IPCHANGE: %s -> %s\n' "$previous_ip" "${lines[1]}"
	fi
}

run_with_sops() {
	local status

	if "$sops_bin" exec-env --pristine "$config_file" \
		'/usr/local/lib/duckdns/update.sh --decrypted' 2>/dev/null; then
		return 0
	else
		status=$?
	fi
	if [[ $status -ne $logged_failure_status ]]; then
		failure "could not decrypt configuration or start updater"
	fi
	return "$status"
}

case ${1:-} in
	"")
		run_with_sops
		;;
	--decrypted)
		run_update "$state_file" "$curl_bin"
		;;
	--test-core)
		[[ $# -eq 3 ]] || exit 64
		run_update "$2" "$3"
		;;
	*)
		exit 64
		;;
esac
