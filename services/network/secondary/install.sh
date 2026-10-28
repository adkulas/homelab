#!/bin/bash
# Run only after reviewing docs/secondary-dns.md. Never starts DNS.
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run with sudo at the Pi console' >&2; exit 1; }
[[ $(uname -m) == aarch64 ]] || { echo 'Requires the piloted ARM64 OS' >&2; exit 1; }
. /etc/os-release
[[ $ID == debian && $VERSION_ID == 13 ]] || { echo 'Requires reviewed Debian 13 runtime dependencies' >&2; exit 1; }
bundle=$(cd -- "$(dirname -- "$0")" && pwd)
for path in /opt/technitium-pi /etc/technitium-pi /var/lib/technitium-pi /etc/systemd/system/technitium-pi.service /etc/systemd/system/technitium-pi-firewall.service; do
  [[ ! -e $path && ! -L $path ]] || { echo 'Existing installation; stop and inspect privately' >&2; exit 1; }
done
! getent passwd technitium-pi >/dev/null || { echo 'Service account already exists; inspect privately' >&2; exit 1; }
! getent group technitium-pi >/dev/null || { echo 'Service group already exists; inspect privately' >&2; exit 1; }
/usr/sbin/nft --check --file "$bundle/firewall.nft"
"$bundle/runtime/dotnet" --list-runtimes | grep -q '^Microsoft.AspNetCore.App 10.0.13 '
useradd --system --user-group --home-dir /var/lib/technitium-pi --shell /usr/sbin/nologin technitium-pi
install -d -m 0755 /opt/technitium-pi
cp -a -- "$bundle/app" "$bundle/runtime" /opt/technitium-pi/
chown -R root:root /opt/technitium-pi
chmod -R a+rX,go-w /opt/technitium-pi
install -d -o root -g technitium-pi -m 0750 /etc/technitium-pi
install -o root -g technitium-pi -m 0640 "$bundle/bootstrap.env" /etc/technitium-pi/bootstrap.env
install -o root -g root -m 0600 "$bundle/firewall.nft" /etc/technitium-pi/firewall.nft
install -d -o technitium-pi -g technitium-pi -m 0700 /var/lib/technitium-pi
install -m 0644 "$bundle/technitium-pi.service" "$bundle/technitium-pi-firewall.service" /etc/systemd/system/
systemctl daemon-reload
printf '%s\n' 'Installed stopped and disabled. Follow secondary-dns.md before first start.'
