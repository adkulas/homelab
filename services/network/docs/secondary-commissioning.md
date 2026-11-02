# Pi DNS commissioning — #104

Commissioned on 2026-10-28–29 (America/Toronto), with live DNS, HTTPS and service
state reverified at closeout on 2026-11-01. The `raspberrypi-dns` Stack Instance
runs natively on the Pi 3 Model B Host, using Debian 13 ARM64 and NetworkManager,
at the static address referenced by `raspberrypi-address` in encrypted Site Inventory.

The implementation in `9f13e54` and factory-scope correction in `0878753` are already
on main. Technitium **15.6.2** and ASP.NET Core **10.0.13 ARM64** use the archive
checksums in [native.py](../secondary/native.py). No version change was made during
commissioning or the removal/reinstall rehearsal.

## Acceptance evidence

| #104 requirement | Observed evidence |
| --- | --- |
| Pinned native systemd service at the verified static Host address | The static NetworkManager profile, direct-address SSH, gateway and independent Host DNS passed after reboot. `technitium-pi.service` is active and enabled; its required firewall service is active. |
| Independent recursive DNS and DNSSEC | Live API readback confirmed no forwarders or proxy and the approved explicit LAN recursion ACL. External UDP/TCP queries passed after reboot, removal/reinstall and closeout. Signed-domain validation passed; the deliberately invalid signed test returned SERVFAIL. |
| Trusted management HTTPS | The live endpoint served the exact issued `raspberrypi.home` certificate with verified chain, name and expiry. The operator imported the public root into Firefox and confirmed browser trust without a connection exception. HTTPS passed again after reboot and reinstall. |
| DHCP remains disabled | API readback showed no enabled scopes. The factory `Default` scope was disabled, confirmed by its Enable button. No UDP 67 listener was observed during live checks or any soak sample; the unit denies UDP 67 binding and the firewall drops server replies. |
| Restricted LAN/VPN access | Approved LAN DNS and HTTPS passed; direct LAN HTTP was denied. An IPv6 link-local SSH positive control established reachability before TCP/UDP DNS and HTTPS denial were verified. No additional VPN or IPv6 client ranges were approved; the firewall and recursion ACL retain the declared LAN-only policy. These are source-policy checks, not an audit of every physical VPN/guest path. |
| Pi CPU, memory, storage and power | Both short workloads and the full 24-hour soak passed the deployment guardrails below, with no service restart, reboot, power warning or kernel storage error. |
| Host network rollback | The armed NetworkManager rollback restored the original DHCP connection successfully. The corrected static profile was reactivated and retained across reboot. The original profile and private snapshots were preserved. |
| Service removal and recovery | A stopped-state encrypted backup was taken; the removal script stopped/disabled the service, removed binaries and units, and left no DNS, management or DHCP listeners. Private configuration, credentials and owning account remained. Reinstalling the same pinned binaries restored trusted HTTPS, independent DNS, firewall dependency and boot startup. API checks again showed no enabled scopes. All operations targeted the Pi; Bell and homelab configuration were not changed. |
| Recoverable backup | The encrypted archive was copied to private PC storage, decrypted there, and checked for configuration, credentials, Pi TLS leaf and both systemd units. It contained no CA root private key. This was a Pi stopped-state recovery archive, not a paired DNS Cluster Restore Drill. |

## Workload and completed soak

Both workloads used 800 queries with eight concurrent workers and returned zero
errors. Cached-query p95 was **10.44 ms**; filtered-query p95 was
**8.67 ms**, below the 100 ms guardrail. The temporary `example.com` block
was removed, and normal resolution was verified afterward.

The soak ran from **2026-10-29 02:31:59 UTC** to **2026-10-30 02:32:00 UTC**:
**24 hours, 97 samples at 15-minute intervals, zero failed checks**. One boot and
one service process were observed throughout, with zero service restarts.

| Metric | Observed | Guardrail |
| --- | --- | --- |
| Service RSS | 135.73–173.36 MiB | Below 256 MiB |
| Available Host memory | At least 646.12 MiB | At least 256 MiB |
| Free storage | At least 3.101 GiB | At least 1 GiB |
| Maximum interval-average service CPU, normalized across Host CPUs | 0.051% | No sustained saturation |
| Power/throttling and kernel storage errors | None observed | No flags or errors |

An interim review accepted 23 healthy hours early at the operator's request. The
monitor continued and completed the full 24 hours; this final record uses all 97
samples rather than relying on the shortened-window exception.

## Approved exceptions and scope limits

- The operator explicitly waived the Ethernet-only wired recovery test. Recovery
  access was available but was not independently exercised over a wired client.
- The operator explicitly authorized temporary custody of the encrypted CA root
  on the connected administrator PC, outside Git, as an exception to ADR-0008.
  Its encrypted backup was decrypted and used for a disposable signing drill.
  Moving the root offline and copying its backup to independent media remain
  follow-up work. No CA root private key was transferred to either DNS Host.
- Limited Pi commissioning proceeded with the operator's exact-plan approval while
  full Site preflight manual confirmations remained incomplete. The original
  report and exceptions were retained privately; those checks were not marked
  passed. IPv6 checks found no global address, default route or IPv6 DNS server;
  three router solicitations observed no advertisements, a bounded observation.
  Household DNS/DHCP handoff and the full Site cutover audit remain separate work.

These approved exceptions remain explicit in the acceptance record. DNS Cluster
membership, permanent local zones and household DNS/DHCP advertisement are separate
tickets. No Bell DHCP handoff was performed for #104.

## Private recovery locations and current operation

Private Pi records and encrypted rehearsal archives are under
`/var/lib/homelab-recovery/issue-104*`; redacted checks, the completed soak and the
operator-owned encrypted backup copy are under
`$HOME/.cache/homelab-issue-104/` on the Pi. Verified PC recovery evidence and the
copied encrypted archive are under `$HOME/.local/state/homelab/issue-104/`.
The CA workspace is separately held under
`$HOME/.local/share/homelab-management-ca-104/` on the administrator PC. Keep its
custody and recovery passwords separate from the Pi service archive.

Installed paths are `/opt/technitium-pi`, `/etc/technitium-pi` and
`/var/lib/technitium-pi`. To stop this Stack Instance while retaining configuration:

```bash
sudo systemctl stop technitium-pi.service
```

Use the [deployment and recovery guide](secondary-dns.md) for the documented
removal path and the private packet for the reviewed Host rollback. The first-start
installer intentionally refuses existing state; do not reset the commissioned
configuration by rerunning an initial-install helper. Future recovery or upgrades
must use a newly reviewed plan and a current protected backup.
