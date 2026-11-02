# Primary DNS commissioning — #103

Commissioned on 2026-11-01 (America/Toronto). The `homelab-dns` Stack Instance is
running on the homelab Host at the static address referenced by `homelab-address`
in the encrypted home Site Inventory. Bell retains DHCP, its gateway, subnet and
advertised client settings. This deployment does not perform a household DNS/DHCP
handoff or join the DNS Cluster.

The production Compose artifacts were already implemented in #103's merged change.
Commissioning used the bundle captured at
`55e31c10ff9f7f8e9f4d2e10a66240ccde0b4c87`, with the pinned Technitium 15.6.2
image/digest in [technitium.env](../technitium.env). The worktree's starting point was
`2a52c9f8b60c094e13afdf952bd63cafb75b12b8`.

## Operator decisions and installed state

- The operator approved the exact address/resolver/access plan over SSH, with the
  physical keyboard/display available as fallback. An independent five-minute Host
  rollback remained armed until a separately pinned SSH connection verified the
  new address, default route, Host DNS and required services.
- Persistent runtime state uses the existing **unencrypted SSD**, explicitly chosen
  by the operator instead of the runbook's encrypted-storage default. Directories
  are root-owned mode 0700; credentials are mode 0600. Recovery archives and private
  credential exports are encrypted outside Git.
- DNS and management access allow the declared home LAN. Additional VPN and IPv6
  client ranges were not approved. These Host input restrictions do not block a
  household client's outbound NordVPN connection; its own DNS/local-access policy
  remains a client setting.
- `systemd-resolved` remains the Host resolver with the independent upstreams saved
  in Netplan. Its stub listener is disabled by
  `/etc/systemd/resolved.conf.d/60-homelab-dns.conf` (mode **0644**, readable by
  `systemd-resolve`). `/etc/resolv.conf` points to
  `/run/systemd/resolve/resolv.conf`.
- Root-private Compose, environment, administrator credential, PFX password and
  public certificate files live under `/etc/homelab-dns`. The bind-mounted
  configuration is `/var/lib/homelab-dns/config`, with the issued leaf at
  `tls/management.pfx`. No CA root private key was deployed to homelab.
- `homelab-dns-firewall.service` loads the dedicated nftables table before Docker.
  Docker has explicit `Requires` and `After` dependencies on that unit. Existing
  forwarding rules and other Compose projects were preserved.

## Acceptance evidence

| Required behavior | Observed result |
| --- | --- |
| Read-only preflight before changes | Encrypted Site Inventory validated; privileged route/Docker inspection completed; the initial loopback DNS conflict was detected and resolved. Strict first-start preflight passed before initial startup. |
| Static addressing and timed rollback | The first failed supervised change restored the prior Netplan, resolver and firewall state with no reported restoration failures; the prior SSH path worked again. The corrected change committed only after new-address SSH and health checks passed. |
| Local DNS over UDP and TCP | A temporary `deployment-test.home` A record returned the exact declared Host address from the Host and laptop. The zone was removed afterward; factory local answers also passed after reboot. Permanent `home` records remain #105's scope. |
| Independent uncached recursion and DNSSEC | No proxy or forwarders; explicit LAN recursion ACL. Cache-flushed external lookups succeeded; a signed domain validated and the deliberately invalid signed test returned SERVFAIL. Checks passed again after reboot. |
| Trusted management HTTPS | `https://homelab.home:53443` served the issued leaf with verified name, chain, expiry and exact certificate. The operator confirmed browser trust without an exception. Plain HTTP 5380 was denied from the LAN. |
| Approved/denied access | Approved LAN DNS and HTTPS passed. Temporary isolated probe links established connectivity before verifying denied IPv4/IPv6 access, then were removed. These test Host source policy, not every possible physical guest/VPN path. |
| Bell remains DHCP owner | No enabled Technitium scopes and no UDP 67 listener. DISCOVER-only probes observed Bell alone with unchanged gateway/DNS options before and after startup and reboot; they did not request leases or change client profiles. |
| Existing services and persistence | Static address, independent Host DNS, trusted HTTPS, DNS/DNSSEC, DHCP-disabled state, source restrictions and configured container restart behavior passed after reboot. Firewall activation preceded Docker. The operator reconfirmed selected household services. |
| Recovery copy | The initial primary-only archive was copied to the laptop, checksum-checked, decrypted and validated down to ZIP members including authentication, DNS and web-service configuration. This is not a paired two-node backup or a new Restore Drill. |

The final DNS reboot check reported every check as passed, including independent LAN
checks, LAN HTTP denial and household services. Its temporary one-shot validation unit
and WantedBy link were removed. The primary retains `unless-stopped` restart behavior.

## Private evidence and ongoing operation

The private operator packet on the laptop is
`$HOME/.local/state/homelab-dns/commissioning-103/`; it contains the encrypted
pre-change recovery packet, reviewed candidate, encrypted credentials, per-operation
checksums, first-start result and `final-reboot-check.json`. Protected Host copies and
operation records are under `/var/lib/homelab-dns/commissioning`.

The verified initial primary archive is
`primary-initial-20261102T003350Z-7ccf5e14.tar.age`, SHA-256
`674ec96ce5b036c6582b913bafafa4bfa35e4ba66930f63a43cd06ce7ced774b`.
Keep the age identity recoverable independently of homelab. Use the
[paired backup procedure](backup-restore.md) before later protected changes involving
both DNS Stack Instances. Keep the CA's custody and recovery procedure separate.

After commissioning, the operator replaced the generated administrator login with
an operator-selected username and password using the private local prompt. The
prompt verifies a fresh administrator login and saves the current credentials in
`operator-login.age` in the laptop packet, encrypted to the Site recovery recipient.
The initial backup and Host bootstrap password predate this change; they do not
represent the current login. Preserve the encrypted credential recovery files and
take a fresh service backup before relying on a backup for the current authentication
state. Historical first-start helpers assume the bootstrap credentials and must not
be rerun against the commissioned service.

Use the private `ssh-config` in that packet for the pinned static-address SSH path.
Container administration still requires password-based sudo. Inspect this project's
status without re-running a first-start gate against occupied service ports:

```bash
sudo docker compose --env-file /etc/homelab-dns/runtime.env \
  -f /etc/homelab-dns/compose.yaml ps
```

For removal/recovery, follow [the primary DNS rollback procedure](primary-dns.md#6-rollback-and-acceptance-record)
using the saved private packet. Do not reset live configuration with a commissioning
helper. Local names, cluster membership and DHCP handoff remain their separate tickets.
