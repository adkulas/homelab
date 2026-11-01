# Primary DNS commissioning readiness — #103

Read-only inspection on 2026-10-31 (America/Toronto), using branch
`feat/103-primary-dns-commission` based on
`2a52c9f8b60c094e13afdf952bd63cafb75b12b8`.
The deployment artifacts already exist from #103's merged implementation.
This record does not approve a live change or establish live acceptance.

## Observed readiness

| Check | Result | Evidence |
| --- | --- | --- |
| Controller Site Inventory validation | Pass | `go run ./cmd/homelab site validate --site home --output json`: valid, no diagnostics; operational values redacted |
| Primary deployment artifact checks | Pass | `go test ./tests/acceptance -run '^TestNetworkPrimaryDeployment$' -count=1` |
| Host access | Pass | Key-authenticated SSH as `adam@homelab` |
| Host tools | Present | Python 3, Docker CLI, nftables, dig and Netplan found |
| Host services | Active | systemd-resolved, Docker and SSH |
| Host checkout | Older than candidate | `cefb8f0f2b952d860b62aa97bf58e639f740e60b`; no tracked changes reported by `git status --porcelain` |
| Strict first-start preflight | Blocked | Exit 2; DNS port conflict, Docker route probe unavailable and unconfirmed operator evidence |

The Host preflight ran from its existing checkout without sudo. Its output reports
loopback TCP/UDP DNS listeners and a DHCP-derived Ethernet default route.
`systemd-resolved` is active and `/etc/resolv.conf` points to its stub file.
Do not disable it before preparing and reviewing an independent Host resolver path.
The deployment will need the verified static address from encrypted Site Inventory;
no operational addresses or MACs are recorded here.

`sudo -n true` did not succeed. Docker CLI availability does not establish Docker
socket access; privileged Docker network inspection remains unverified. Use the
operator's password-based sudo session for privileged work. Do not grant Docker
group membership or weaken sudo policy to bypass this requirement.

## Remaining behavior and evidence

| Public seam | Required behavior and source | Evidence needed before acceptance |
| --- | --- | --- |
| Strict first-start preflight | #103: detect port/route conflicts before changes | Successful privileged network inspection, resolved port 53 conflict, dated private confirmations, exit 0 immediately before start |
| Host addressing and recovery | #103; ADR-0007: verified static address and timed local rollback | Tested physical console and wired recovery client, saved profiles, reviewed exact private change plan, rollback rehearsal and reboot checks |
| Primary DNS Stack Instance | #103: independent recursion with DHCP disabled | Fresh private state, pinned image, local UDP/TCP answers, uncached external recursion and DNSSEC checks; no enabled scopes or UDP 67 listener |
| Management and household access | #103: trusted HTTPS and approved LAN/VPN restrictions | Trusted leaf installed; permitted/denied IPv4/IPv6 probes; persistent firewall before Docker; existing SSH, Docker, gateway and household service checks |
| Rollback | #103: restore prior Host state and retain Bell subnet/gateway | Supervised service/address removal rehearsal; repeated connectivity checks; Bell remains DHCP owner |

Follow [the primary DNS procedure](primary-dns.md) for the exact ordered steps.
First establish physical recovery access and save the private recovery packet.
Review approved client/administrator networks, the resolver replacement, static
address candidate, firewall persistence and timed rollback together as an exact
private change plan before applying it. Confirm current Bell leases exclude the
selected address; inventory validation alone cannot prove it is unused live.

SSH access is available, but it does not confirm the physical recovery prerequisites.
No Host profile, resolver setting, firewall, Bell setting or production container
was changed during this inspection. Keep #103 open until its live checks pass;
record private evidence separately and publish only redacted results.
