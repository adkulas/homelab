# Home Site private recovery packet

Copy this template to encrypted storage **outside Git** and fill it using the read-only preflight guide. Keep an offline copy available to the wired recovery client. Record actual addresses and MACs only in that private copy.

## Bell Giga Hub — current state

- Admin access from wired client: [private location / procedure]
- Current LAN gateway and subnet: [record privately]
- DHCP enabled state, pool, lease time, DNS options, and reservations: [record privately]
- Current lease export or screenshots: [private location]
- IPv6 RA/RDNSS and DNS behavior: [record privately]
- Port forwards, firewall, guest/Bell TV behavior: [record privately]
- DHCP disable and re-enable page/control: [record exact UI path privately]
- Backup of current Bell settings or screenshots: [private location]

## Host network state

| Host | Current profile backup | Ethernet/Wi-Fi routes and metrics | Local console | Prior connection name | Rollback verified |
| --- | --- | --- | --- | --- | --- |
| homelab | [private location] | [private location] | [method] | [name] | [yes/no] |
| raspberrypi | [private location or not installed] | [private location] | [method] | [name] | [yes/no] |

Record current Netplan files for `homelab` and active NetworkManager profiles for `raspberrypi`, including ownership and permissions. Record the route and resolver snapshots from the guide. Capture the service listeners that need review before Technitium starts.

## Address and service checks

- Proposed static and reservation address conflict comparison: [private evidence location / result]
- Other DHCP responder check: [method / result]
- IPv6 DNS bypass audit: [method / result]
- Hard-coded address review, including Docker, Traefik, Minecraft, DuckDNS, Tailscale, firewall, NVR, and cameras: [private notes]
- Wired recovery client can reach Bell and both Hosts without new DNS: [method / result]

## Fallback order

1. If a Host address change loses access, use its **local console** and the saved network profile. On Ubuntu, allow `netplan try` to time out or restore the prior Netplan files and run `sudo netplan apply`. On the Pi, restore the prior NetworkManager profile, reload connections, and activate the recorded prior connection.
2. If a DHCP cutover fails, **disable Technitium DHCP first** and verify it no longer offers leases.
3. Use the wired client to restore the recorded Bell DHCP settings and enable Bell DHCP. Keep Bell's LAN gateway and subnet unchanged.
4. Renew one test client's lease. Verify Bell is the only DHCP server, the recorded gateway works, and external DNS resolves. Then inspect other clients.

Do not begin any live network change while a required field above is missing or the local recovery path is untested.
