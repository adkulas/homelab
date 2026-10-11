#!/bin/bash

set -euo pipefail

readonly service_user=duckdns
readonly service_group=duckdns
readonly secrets_group=sops
readonly key_file=/etc/sops/age/keys.txt

if [[ $EUID -ne 0 ]]; then
	printf 'install.sh must be run as root (use sudo)\n' >&2
	exit 1
fi

source_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

for dependency in cmp env getent groupadd install mv rm runuser stat systemctl useradd usermod; do
	if ! command -v "$dependency" >/dev/null 2>&1; then
		printf 'required dependency is unavailable: %s\n' "$dependency" >&2
		exit 1
	fi
done

for executable in /bin/bash /usr/bin/awk /usr/bin/curl /usr/bin/journalctl /usr/bin/mv /usr/bin/rm /usr/bin/tr /usr/local/bin/sops; do
	if [[ ! -x $executable ]]; then
		printf 'required executable is unavailable: %s\n' "$executable" >&2
		exit 1
	fi
done

for source_name in secrets.sops.yaml update.sh logs.sh duckdns.service duckdns.timer; do
	if [[ ! -f $source_directory/$source_name ]]; then
		printf 'required source file is missing: %s\n' "$source_directory/$source_name" >&2
		exit 1
	fi
done

if ! getent group "$secrets_group" >/dev/null; then
	groupadd --system "$secrets_group"
fi
if ! getent group "$service_group" >/dev/null; then
	groupadd --system "$service_group"
fi
if ! getent passwd "$service_user" >/dev/null; then
	useradd \
		--system \
		--gid "$service_group" \
		--groups "$secrets_group" \
		--home-dir /var/lib/duckdns \
		--shell /usr/sbin/nologin \
		"$service_user"
else
	usermod --append --groups "$secrets_group" "$service_user"
fi

install -d -o root -g "$service_group" -m 0750 /etc/duckdns
install -d -o root -g root -m 0755 /usr/local/lib/duckdns
install -d -o "$service_user" -g "$service_group" -m 0700 /var/lib/duckdns

if [[ ! -f $key_file ]]; then
	printf 'SOPS age identity is missing: %s\n' "$key_file" >&2
	printf 'install the shared identity as root:%s mode 0640, then rerun install.sh\n' "$secrets_group" >&2
	exit 1
fi
key_metadata=$(stat -c '%U:%G:%a' "$key_file")
if [[ $key_metadata != "root:$secrets_group:640" ]]; then
	printf 'unsafe SOPS age identity ownership or mode: %s is %s, expected root:%s:640\n' \
		"$key_file" "$key_metadata" "$secrets_group" >&2
	exit 1
fi

validation_file=/etc/duckdns/validate.$$.sops.yaml
trap 'rm -f -- "$validation_file"' EXIT
install -o root -g "$service_group" -m 0640 "$source_directory/secrets.sops.yaml" "$validation_file"
if ! runuser -u "$service_user" -- \
	env SOPS_AGE_KEY_FILE="$key_file" \
	/usr/local/bin/sops exec-env --pristine "$validation_file" \
	'/usr/bin/test -n "$DUCKDNS_DOMAIN" && /usr/bin/test -n "$DUCKDNS_TOKEN"' \
	>/dev/null 2>&1; then
	printf 'DuckDNS SOPS configuration is not decryptable or contains an empty value: %s\n' \
		"$source_directory/secrets.sops.yaml" >&2
	exit 1
fi
rm -f -- "$validation_file"
trap - EXIT

temporary_files=()
cleanup() {
	local temporary
	for temporary in "${temporary_files[@]}"; do
		if [[ -n $temporary ]]; then rm -f -- "$temporary"; fi
	done
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

file_changed=false
install_file() {
	local source=$1
	local destination=$2
	local mode=$3
	local owner=$4
	local group=$5
	local metadata
	local temporary

	if [[ -f $destination ]]; then
		metadata=$(stat -c '%U:%G:%a' "$destination")
		if cmp -s -- "$source" "$destination" && [[ $metadata == "$owner:$group:$mode" ]]; then
			file_changed=false
			return
		fi
	fi

	temporary="${destination}.tmp.$$"
	temporary_files+=("$temporary")
	install -o "$owner" -g "$group" -m "0$mode" -- "$source" "$temporary"
	mv -fT -- "$temporary" "$destination"
	temporary_files[-1]=
	file_changed=true
}

install_file "$source_directory/secrets.sops.yaml" /etc/duckdns/secrets.sops.yaml 640 root "$service_group"
install_file "$source_directory/update.sh" /usr/local/lib/duckdns/update.sh 755 root root
install_file "$source_directory/logs.sh" /usr/local/bin/duckdns-logs 755 root root
install_file "$source_directory/duckdns.service" /etc/systemd/system/duckdns.service 644 root root
install_file "$source_directory/duckdns.timer" /etc/systemd/system/duckdns.timer 644 root root
timer_changed=$file_changed

timer_was_active=false
if systemctl is-active --quiet duckdns.timer; then
	timer_was_active=true
fi

systemctl daemon-reload
if ! systemctl is-enabled --quiet duckdns.timer; then
	systemctl enable duckdns.timer
fi
if [[ $timer_was_active == true && $timer_changed == true ]]; then
	systemctl restart duckdns.timer
elif ! systemctl is-active --quiet duckdns.timer; then
	systemctl start duckdns.timer
fi

printf 'DuckDNS updater installed; timer status:\n'
systemctl --no-pager --full status duckdns.timer
