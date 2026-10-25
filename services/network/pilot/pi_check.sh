#!/usr/bin/env bash
# Run on the unused Pi 3 as its ordinary user. No sudo or Host network changes.
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
pilot_dir="${XDG_CACHE_HOME:-$HOME/.cache}/technitium-pilot-99"
mkdir -p "$pilot_dir"
chmod 700 "$pilot_dir"
if [[ $(uname -m) != aarch64 ]] || ! tr -d '\0' </proc/device-tree/model | grep -q 'Raspberry Pi 3 Model B'; then
  echo 'This check requires the ARM64 Raspberry Pi 3 Model B' >&2
  exit 1
fi

archive_url=https://download.technitium.com/dns/archive/15.6.2/DnsServerPortable.tar.gz
curl -fsSL "$archive_url" -o "$pilot_dir/DnsServerPortable.tar.gz"
curl -fsSL "$archive_url.sha256" -o "$pilot_dir/DnsServerPortable.tar.gz.sha256"
expected=$(cat "$pilot_dir/DnsServerPortable.tar.gz.sha256")
actual=$(sha256sum "$pilot_dir/DnsServerPortable.tar.gz" | cut -d ' ' -f 1)
if [[ "$actual" != "$expected" ]]; then
  echo 'Pinned Technitium archive checksum failed' >&2
  exit 1
fi
mkdir -p "$pilot_dir/app"
tar -xzf "$pilot_dir/DnsServerPortable.tar.gz" -C "$pilot_dir/app"

if [[ ! -x "$pilot_dir/dotnet/dotnet" ]]; then
  curl -fsSL https://dot.net/v1/dotnet-install.sh -o "$pilot_dir/dotnet-install.sh"
  bash "$pilot_dir/dotnet-install.sh" --version 10.0.13 --runtime aspnetcore \
    --architecture arm64 --install-dir "$pilot_dir/dotnet" --no-path >/dev/null
fi
if ! "$pilot_dir/dotnet/dotnet" --list-runtimes | grep -q 'Microsoft.AspNetCore.App 10.0.13'; then
  echo 'Pinned ASP.NET runtime is unavailable' >&2
  exit 1
fi

if [[ ! -x "$pilot_dir/nettools/usr/bin/slirp4netns" ]]; then
  mkdir -p "$pilot_dir/netpkgs" "$pilot_dir/nettools"
  (
    cd "$pilot_dir/netpkgs"
    apt download 'slirp4netns=1.2.1-1.1' 'libslirp0=4.8.0-1+deb13u1' >/dev/null
    for package in ./*.deb; do dpkg-deb -x "$package" "$pilot_dir/nettools"; done
  )
fi
helper="$pilot_dir/nettools/usr/bin/slirp4netns"
export PILOT_DIR="$pilot_dir"
export PILOT_PROBE="$script_dir/pi_probe.py"
umask 077
head -c 32 /dev/urandom | base64 > "$pilot_dir/admin-password"
rm -f "$pilot_dir/net-ready"
rm -rf "$pilot_dir/config"
namespace_pid=
slirp_pid=
cleanup() {
  [[ -z "$slirp_pid" ]] || kill "$slirp_pid" 2>/dev/null || true
  [[ -z "$namespace_pid" ]] || kill "$namespace_pid" 2>/dev/null || true
  rm -f "$pilot_dir/net-ready" "$pilot_dir/admin-password" "$pilot_dir/native.log" "$pilot_dir/slirp.log"
  rm -rf "$pilot_dir/config"
}
trap cleanup EXIT

unshare -Urn bash "$script_dir/pi_inner.sh" &
namespace_pid=$!
sleep 1
LD_LIBRARY_PATH="$pilot_dir/nettools/usr/lib/aarch64-linux-gnu" \
  "$helper" --configure --disable-host-loopback "$namespace_pid" tap0 \
  > "$pilot_dir/slirp.log" 2>&1 &
slirp_pid=$!
sleep 2
if ! kill -0 "$namespace_pid" 2>/dev/null || ! kill -0 "$slirp_pid" 2>/dev/null; then
  echo 'Pi network namespace did not start' >&2
  exit 1
fi
touch "$pilot_dir/net-ready"
wait "$namespace_pid"
