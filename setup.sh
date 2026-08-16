#!/bin/sh

set -eu

operating_system=$(uname -s)
if [ "$operating_system" != "Linux" ]; then
	printf 'unsupported operating system: %s; setup.sh supports Ubuntu and WSL 2\n' "$operating_system" >&2
	exit 64
fi

machine=$(uname -m)
case "$machine" in
	x86_64)
		architecture=amd64
		;;
	aarch64 | arm64)
		architecture=arm64
		;;
	*)
		printf 'unsupported Linux architecture: %s; setup.sh supports amd64 and arm64\n' "$machine" >&2
		exit 64
		;;
esac

for dependency in curl sha256sum; do
	if ! command -v "$dependency" >/dev/null 2>&1; then
		printf 'required launcher dependency is unavailable: %s\n' "$dependency" >&2
		exit 1
	fi
done

version=v0.1.0
asset="media-stack_${version#v}_linux_${architecture}"
release_url="https://github.com/adkulas/homelab/releases/download/${version}"
cache_root=${XDG_CACHE_HOME:-${HOME:?HOME must be set when XDG_CACHE_HOME is unset}/.cache}
cache_directory="${cache_root}/media-stack/${version}"
binary_path="${cache_directory}/${asset}"
checksum_name="${asset}.sha256"
checksum_path="${cache_directory}/${checksum_name}"

mkdir -p "$cache_directory"
if [ ! -f "$binary_path" ] || [ ! -f "$checksum_path" ]; then
	binary_download="${binary_path}.download.$$"
	checksum_download="${checksum_path}.download.$$"
	trap 'rm -f "$binary_download" "$checksum_download"' EXIT HUP INT TERM
	curl -fsSL "${release_url}/${asset}" -o "$binary_download"
	curl -fsSL "${release_url}/${checksum_name}" -o "$checksum_download"
	mv "$binary_download" "$binary_path"
	mv "$checksum_download" "$checksum_path"
	trap - EXIT HUP INT TERM
fi

checksum_valid=false
expected_hash=
expected_asset=
unexpected_checksum_content=
if IFS=' ' read -r expected_hash expected_asset unexpected_checksum_content <"$checksum_path"; then
	if actual_checksum=$(sha256sum "$binary_path"); then
		actual_hash=${actual_checksum%% *}
		[ "$expected_asset" = "$asset" ] && [ -z "$unexpected_checksum_content" ] && [ "$expected_hash" = "$actual_hash" ] && checksum_valid=true
	fi
fi
if [ "$checksum_valid" != true ]; then
	rm -f "$binary_path" "$checksum_path"
	printf 'CLI checksum verification failed for %s\n' "$asset" >&2
	exit 1
fi

chmod 755 "$binary_path"
exec "$binary_path" init "$@"
