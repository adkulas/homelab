# Home Site preflight and recovery packet

This is the read-only inventory stage. Bell remains the gateway and DHCP server on its existing LAN. The operator has narrowed Bell's current DHCP pool; record and verify its current bounds in the private packet. The proposed Host addresses and Technitium DHCP scope are **not** applied here.

## 1. Prepare private evidence storage

Choose a directory on an encrypted PC disk or encrypted removable drive, **outside this Git repository**, with access limited to the operator. Copy [the recovery packet template](recovery-packet.template.md) there and record its location on paper so it remains findable if `homelab` fails. Keep any router screenshots, lease exports, Netplan files, NetworkManager profiles, routes, and service notes there; they may contain IPs, MACs, or credentials. Do not attach them to a GitHub issue or copy them into the redacted report.

For each Host, save the current network files and the output of `ip -j address`, `ip -j route`, `ip -j -6 route`, and `resolvectl dns` privately before any later address change. On Ubuntu, preserve all files under `/etc/netplan/` and note the active Ethernet and Wi-Fi profiles. On a current Raspberry Pi OS, preserve the active NetworkManager connection profile and `nmcli connection show` output. If the Pi has no OS yet, record that fact and leave its Host checks unresolved until installation.

The operator's redacted Ubuntu Netplan output confirms that both Ethernet and Wi-Fi use DHCPv4. It declares no static address,
gateway, DNS server, or route metric. The read-only route check observed Ethernet as the preferred default route and Wi-Fi as
a lower-priority default route. Preserve the root-owned original Netplan file privately, including its existing Wi-Fi credentials, before any
later edit. The Raspberry Pi's redacted `netplan get` output declares only NetworkManager as its renderer. The active Ethernet
profile is `Wired connection 1` on `eth0`; `ipv4.method` is `auto`, with no static address, gateway, or DNS values in that
profile. Preserve the active profile and effective routes privately before changing the Pi address. Netplan also warned that
its NetworkManager renderer file under `/lib/netplan/` has overly broad permissions; inspect that file's ownership and mode
before a later Pi configuration change.

## 2. Capture Bell and Site state

Log in to the Bell Giga Hub locally. **Read and record**, without pressing Save:

1. The current LAN gateway, subnet, DHCP enabled state, pool bounds, lease time, reservations, and current lease list.
2. The DHCP disable and re-enable controls and the current DNS and IPv6 settings, including any IPv6 DNS advertisement to clients.
3. Port forwards, firewall rules, Bell TV or guest network behavior, and any device-specific assignments.

Compare the proposed encrypted address inventory with the current leases and each statically configured device. Check the NVR, cameras, desktop, printers, and other fixed equipment. Mark a planned address free only after this comparison; a ping with no reply is insufficient. Inspect the current client DHCP server identifier and verify that no other responder is active on the main LAN. Review hard-coded addresses in Docker, Traefik, Minecraft forwarding, DuckDNS, Tailscale, firewall rules, NVR, cameras, and client settings. Review the paths in `hardcodedAddressCandidateFiles` from the report; the scanner covers tracked Site and service configuration only.

Connect a recovery client by Ethernet. Confirm it can reach the Bell admin page and SSH or console paths to the Hosts with ordinary DNS unavailable. Identify a local keyboard/display or other console for each Host. Record the current Bell values and the exact controls needed to restore Bell DHCP in the private packet.

## 3. Run and read the redacted checks

From a repository checkout **on the named machine**:

```bash
python3 services/network/preflight.py --role homelab > /path/outside/git/homelab-preflight.json
python3 services/network/preflight.py --role raspberrypi > /path/outside/git/raspberrypi-preflight.json
python3 services/network/preflight.py --role recovery-client > /path/outside/git/recovery-client-preflight.json
```

Each command initially exits `2` because operator confirmations are missing. The report lists every unresolved check under `blockers`. A missing tool or missing default route also creates a blocker. The report shows route interfaces and metrics, address presence, listener binding types, IPv6 resolver presence, profile counts, and filenames needing review. It cannot prove Bell's DHCP state or that an address is unused.

Create a private `evidence.json` file outside Git with boolean confirmations. Set a value to `true` **only after** recording its evidence in the private packet. Use the blocker names in the report, lowercased, as keys; for example:

```json
{
  "bell_settings_saved": true,
  "planned_addresses_checked": true,
  "wired_recovery_client_tested": true
}
```

Pass it as `--evidence /path/outside/git/evidence.json` and rerun. Separate evidence files per Host are acceptable. The script deliberately ignores extra prose or network details: the evidence file accepts only boolean values. Retain the private evidence and redacted reports together, but keep the former encrypted. A zero exit status is a **preflight result**, not authorization to change the network.

## 4. Recovery sequence to keep in the packet

Before any later Host address change, test the local console. For Ubuntu, use `sudo netplan try` for a timed change; if connectivity fails, let it revert. If a saved configuration must be restored at the console, restore the preserved Netplan files, then run `sudo netplan apply` and verify gateway and SSH. Preserve the existing Wi-Fi and default-route configuration. For Raspberry Pi OS with NetworkManager, restore the preserved connection profile, run `sudo nmcli connection reload`, then activate the saved profile by its recorded name from the local console.

If a later DHCP cutover fails, use the wired recovery client and local Host console. **Disable Technitium DHCP first** and verify it no longer answers. Then open the Bell DHCP settings, restore the recorded prior pool, DNS, lease and reservation values, enable Bell DHCP, and save. Renew a test client's lease and confirm that its server identifier is Bell, its gateway works, and external DNS resolves. Do not enable Bell DHCP while Technitium is still answering. Bell's LAN gateway and subnet do not change in this project.

Stop before any live change if the private packet is incomplete, a proposed address conflicts with a lease or static device, an IPv6 DNS path is unexplained, a required service port has an unexplained listener, or the wired/local-console recovery route fails.

## Strict primary first-start gate

For #103, run `python3 services/network/preflight.py --role homelab --deployment`
to inspect blockers before any Host change, then require exit 0 immediately before
starting the production container. This additionally blocks
on any observed DNS/DHCP/HTTP/HTTPS listener, including loopback, and on overlap between
Docker IPAM and IPv4 networks on default-route interfaces. Failed Docker probes block.
It also requires dated
route, firewall, rollback and fresh-state confirmations. Boolean evidence cannot waive
an observed conflict or failed probe. See [the primary procedure](primary-dns.md).
This first-start gate is not a health check for an already running DNS Stack Instance.
