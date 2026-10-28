#!/bin/bash
# Preserve private configuration, credentials and their owning account for recovery.
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run with sudo at the Pi console' >&2; exit 1; }
systemctl disable --now technitium-pi.service
systemctl disable --now technitium-pi-firewall.service
rm -f /etc/systemd/system/technitium-pi.service /etc/systemd/system/technitium-pi-firewall.service
systemctl daemon-reload
rm -rf -- /opt/technitium-pi
# Remove only this deployment's table, after DNS has stopped.
if nft list table inet technitium_pi >/dev/null 2>&1; then
  nft delete table inet technitium_pi
fi
printf '%s\n' 'Removed service and binaries; retained /etc/technitium-pi, /var/lib/technitium-pi and service account.'
