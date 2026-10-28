# Native DNS on raspberrypi — #104

This procedure installs `raspberrypi-dns` on the existing Pi 3 Model B running Debian 13
ARM64 with NetworkManager. The #99 pilot verified this OS/architecture; retain it instead
of reimaging to the plan's initial 32-bit suggestion. Another architecture requires a
separate runtime pin and pilot. Do not run the namespace pilot as a production installer.
Bell remains the gateway and sole DHCP responder. Keep homelab and client DNS unchanged.
DNS Cluster membership, permanent zones and household DNS advertisement belong to later
tickets. This DNS-only installation creates no DHCP scope.

Commands run at the **Pi console** from this checkout unless a different machine is
named. SSH via `ssh adam@rasp-pi` can do read-only preparation; address changes require
the tested local console and wired recovery client. Do not share passwords in chat.
Use `set +x; umask 077`. All uppercase paths below must be absolute paths outside Git
on private storage. Keep the recovery packet on both the Pi and recovery PC. Do not
publish addresses, MACs, profiles, firewall content, certificates' private keys or logs.

## Behavior and evidence

| Public seam | Required behavior and source | Observable evidence |
| --- | --- | --- |
| `preflight.py --role raspberrypi --deployment` | #104: routes, free ports, recovery/access/fresh-state confirmations; native Host needs no Docker | Fixture CLI checks plus dated private live report |
| `secondary/native.py stage` and generated install/removal commands | #104/#99: checksum-pinned native release/runtime; fresh state; stopped systemd install; private credentials; preserved recovery data | Tamper/ACL rejection checks, real-archive staging, unit validation, live start/removal rehearsal |
| Pi console and recovery client | #104, ADR-0007/0008: static address, independent recursive DNS, trusted HTTPS, restricted LAN/VPN access, DHCP absent and working rollback | Private acceptance record and supervised reboot/soak |

Repository artifacts and automated tests do not complete live acceptance. Keep #104
open until the complete private checklist passes. The verified pins are Technitium
15.6.2 (SHA-256 in `native.py`) and ASP.NET Core/.NET 10.0.13 ARM64 (SHA-512 in `native.py`).
Sources: [vendor release archive](https://download.technitium.com/dns/archive/15.6.2/DnsServerPortable.tar.gz.sha256),
[Microsoft release metadata](https://builds.dotnet.microsoft.com/dotnet/release-metadata/10.0/releases.json),
and [first-start settings](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DockerEnvironmentVariables.md).
Bootstrap environment settings initialize fresh state only; later file edits do not
reconcile persisted settings. Never reuse pilot, restored or previously populated state.

## 1. Save state and finish read-only checks — Pi and recovery PC

Complete setup steps 1–5, including encrypted Site validation, Bell lease/pool review,
physical console test and issued Pi leaf. Confirm Bell cannot allocate the selected
static address; compare fixed devices as well as leases. A silent ping is insufficient.
Prepare `PACKET_DIR`, `EVIDENCE`, `PI_ADDRESS`, `PI_PREFIX`, `BELL_GATEWAY`, and
`BOOTSTRAP_DNS` from privately validated inventory and current settings. Bootstrap DNS
must remain reachable without either new DNS service. Retain existing IPv6 policy.
Record the wired interface and active NetworkManager connection UUID as `PI_INTERFACE`
and `ORIGINAL_UUID`; use `nmcli -f NAME,UUID,DEVICE connection show --active` privately.

Save these outputs at the console, using the encrypted/private packet location:

```bash
sudo cp -a /etc/NetworkManager/system-connections "$PACKET_DIR/networkmanager-before"
ip -j address > "$PACKET_DIR/addresses-before.json"
ip -j route > "$PACKET_DIR/routes-before.json"
ip -j -6 route > "$PACKET_DIR/routes6-before.json"
sudo nft list ruleset > "$PACKET_DIR/firewall-before.nft"
python3 services/network/preflight.py --role raspberrypi
```

Privately record active network profiles, default routes/metrics, DNS, firewall manager
and persistence units, SSH/VPN access and any services already using this Pi. Review IPv6
RA/RDNSS on a client. Missing operator confirmations initially return exit 2. Set only
proven booleans in the evidence file described in the preflight guide. Do not waive a
failed probe or conflicting listener. Resolve Wi-Fi fallback/routes before proceeding.
Record baseline gateway, internet, SSH and homelab DNS/service checks on the wired client.

Verify OS with `cat /etc/os-release`, architecture with `uname -m`, power with
`vcgencmd get_throttled`, memory with `free -m`, storage with `df -h /`, and kernel errors
with `sudo journalctl -k -b`. Stop on undervoltage/throttling, SD I/O errors or instability.
The live read-only check on 2026-10-27 found the piloted Debian 13 ARM64 installation,
adequate free memory/storage and `throttled=0x0`; repeat rather than relying on that sample.

**Stop/rollback:** These checks change no network setting. Stop if recovery, certificate
custody, Bell pool exclusions, address ownership or IPv6 policy is unresolved.

## 2. Prepare the exact static-address plan and rollback — Pi console

**Interruption:** Activation may disconnect SSH. ADR-0007 requires separate approval of
this exact private plan before activation; a request to begin #104 is not that approval.
Review current and proposed UUID/interface, address/prefix, gateway, resolver list,
route priorities and IPv6 policy against the private packet. If the declared static
address is already configured correctly, verify it and its recovery path; skip mutation.

Keep the original profile unchanged. Verify there is no existing `raspberrypi-dns-static`
profile, then clone the saved wired profile. These commands stage an inactive candidate:

```bash
sudo nmcli connection clone uuid "$ORIGINAL_UUID" raspberrypi-dns-static
sudo nmcli connection modify raspberrypi-dns-static connection.autoconnect no \
  ipv4.method manual ipv4.addresses "$PI_ADDRESS/$PI_PREFIX" \
  ipv4.gateway "$BELL_GATEWAY" ipv4.dns "$BOOTSTRAP_DNS" ipv4.ignore-auto-dns yes
```

Cloning preserves interface matching, MTU, route metrics and IPv6 settings. Inspect all
candidate and original properties privately with `sudo nmcli connection show ...`.
Resolve unexpected additional routes/addresses or profile-specific behavior before apply.
Record the original autoconnect state and priority; the procedure never changes them.

Create a root-owned mode-0700 rollback script in the packet. Write the actual original
UUID into it locally (UUID is profile metadata); replace `ORIGINAL_UUID_FROM_PACKET`:

```bash
#!/bin/bash
set -euo pipefail
nmcli connection modify raspberrypi-dns-static connection.autoconnect no
nmcli connection up uuid ORIGINAL_UUID_FROM_PACKET
```

Save as `$PACKET_DIR/rollback-pi-address.sh`, set `sudo chown root:root` and
`sudo chmod 0700`, then `sudo bash -n` it. Verify the packet parent is root-owned and
unwritable by other users. Retain the original profile files for recovery if NetworkManager
itself fails. On failure, activate the original locally; if needed restore the saved files
under `/etc/NetworkManager/system-connections`, `sudo nmcli connection reload`, then
`sudo nmcli connection up uuid "$ORIGINAL_UUID"`.

After approval, arm a five-minute persistent rollback and activate the candidate:

```bash
sudo systemd-run --unit=raspberrypi-dns-address-rollback --on-active=5m \
  /bin/bash "$PACKET_DIR/rollback-pi-address.sh"
sudo nmcli connection up raspberrypi-dns-static ifname "$PI_INTERFACE"
```

From the recovery client verify the new address, gateway, internet, SSH and unchanged
homelab services. Rehearse rollback now: run the script locally, prove the original path
works, then re-arm and reactivate the candidate. Reused transient unit names may require
`sudo systemctl reset-failed raspberrypi-dns-address-rollback.service` after a failed run;
wait for a completed transient unit to unload before reusing its name.

After successful checks, give the candidate an autoconnect priority higher than the
original (choose the reviewed integer as `STATIC_PRIORITY`), enable its autoconnect,
and cancel the timer:

```bash
sudo nmcli connection modify raspberrypi-dns-static connection.autoconnect yes \
  connection.autoconnect-priority "$STATIC_PRIORITY"
sudo systemctl stop raspberrypi-dns-address-rollback.timer
```

Reboot under supervision and repeat gateway, internet, SSH, route/DNS and household
checks. On failure run the rollback script at the console. Never change Bell DHCP to
repair this step. Delete the inactive candidate only after the original works.

## 3. Stage and inspect the native bundle — Pi (ordinary user)

After reviewing the OS package plan, update this unused Pi with `sudo apt update` and
`sudo apt upgrade`; preserve a bootable original card/backup and reboot before DNS install.
Install reviewed dependencies using:

```bash
sudo apt install ca-certificates curl python3 libicu76 libssl3t64 libstdc++6 zlib1g \
  nftables dnsutils git iproute2
```

Do not reimage or remove packages from a Pi already running another service. If an OS
update fails, stop and recover the saved card; do not continue to Host/service changes.
Choose a new mode-0700 private `DOWNLOAD_DIR` and a new, nonexistent `BUNDLE_DIR` outside
Git. Set `PI_RECURSION_ACL` to comma-separated canonical authorized LAN/VPN client networks
and `PI_MANAGEMENT_ACL` to authorized administrator/peer networks. Do not assume every
private network is trusted. Explicitly include authorized IPv6 ranges or leave IPv6 denied.

```bash
curl -fsSL https://download.technitium.com/dns/archive/15.6.2/DnsServerPortable.tar.gz \
  -o "$DOWNLOAD_DIR/app.tar.gz"
curl -fsSL https://builds.dotnet.microsoft.com/dotnet/aspnetcore/Runtime/10.0.13/aspnetcore-runtime-10.0.13-linux-arm64.tar.gz \
  -o "$DOWNLOAD_DIR/runtime.tar.gz"
python3 services/network/secondary/native.py stage \
  --app-archive "$DOWNLOAD_DIR/app.tar.gz" --runtime-archive "$DOWNLOAD_DIR/runtime.tar.gz" \
  --recursion-acl "$PI_RECURSION_ACL" --management-acl "$PI_MANAGEMENT_ACL" \
  --output "$BUNDLE_DIR"
```

Expected: verified app/runtime, unit files, bootstrap environment, nftables rules,
`install.sh` and `remove.sh`; no running service, Host mutation or password in the bundle.
The CLI refuses checksum mismatches, wildcard ACLs, existing output and Git output paths.
Failure returns 64; remove an incomplete download and retry only after understanding it.
Inspect the staged ACLs privately. The nft table owns only this Pi DNS access policy:
loopback permitted; selected sources may reach UDP/TCP 53 or TCP 53443; other DNS/management
sources including IPv6 are denied; non-loopback HTTP is denied; DHCP server replies are
dropped. Outbound recursion and the Pi's ordinary DHCP client are preserved.

Review every active firewall chain: an accept here cannot override another chain's drop.
Stop if the existing manager flushes foreign tables or cannot preserve this policy; integrate
and verify persistence before proceeding. The dedicated firewall unit must succeed before
DNS starts, and leaves restrictions in place when DNS stops. No generic SSH/forwarding
policy is replaced. Validate with `sudo nft --check --file "$BUNDLE_DIR/firewall.nft"`.

## 4. Install stopped, create credentials and approve first start — Pi console

Review the exact bundle/version, service account, fixed paths, firewall and bootstrap
settings before applying. Install creates fresh state and refuses any previous installation:

```bash
sudo bash "$BUNDLE_DIR/install.sh"
```

Expected: root-owned binaries under `/opt/technitium-pi`, root-controlled bootstrap/firewall
under `/etc/technitium-pi`, empty service-owned mode-0700 `/var/lib/technitium-pi`, and
**stopped, disabled** units. Installer does not start or enable either unit. It gives the
service only bind-to-low-port capability, removes raw/network administration capabilities,
and adds a UDP 67 bind deny. The firewall additionally blocks DHCP replies. Do not import
configuration, create a scope or relax these restrictions. Stop if installation fails;
inspect partial files locally and use the removal procedure before a clean retry.

Create the administrator password on the Pi through a hidden prompt (not an argument).
The service reads it from a root-controlled, group-readable file:

```bash
sudo bash -c 'set -e; umask 077; read -r -s -p "New DNS admin password: " dns_password </dev/tty; echo >/dev/tty; test -n "$dns_password"; printf "%s\n" "$dns_password" > /etc/technitium-pi/admin-password; chown root:technitium-pi /etc/technitium-pi/admin-password; chmod 0640 /etc/technitium-pi/admin-password'
```

Retain the password in private custody. Do not enter a blank password. Verify the unit:

```bash
sudo systemd-analyze verify /etc/systemd/system/technitium-pi.service \
  /etc/systemd/system/technitium-pi-firewall.service
sudo systemctl start technitium-pi-firewall.service
python3 services/network/preflight.py --role raspberrypi --deployment
```

Inspect effective rules privately. Complete every first-start evidence confirmation only
after checking its dated record, including free ports, routes, fresh state, persistent
firewall and armed rollback. Before first start, separately approve this exact core DNS
plan: pinned version/runtime, explicit ACLs, no forwarders, disabled DHCP, loopback HTTP,
trusted HTTPS configuration in step 5, and the pilot/soak guardrails below. Do not start
if the plan or live state changed after review.

Arm the service rollback, then run the strict preflight immediately before starting:

```bash
sudo systemd-run --unit=raspberrypi-dns-service-rollback --on-active=5m \
  /usr/bin/systemctl stop technitium-pi.service
python3 services/network/preflight.py --role raspberrypi --deployment --evidence "$EVIDENCE" \
  && sudo systemctl start technitium-pi.service
```

Expected: `sudo systemctl is-active technitium-pi.service` reports active. If checks need
longer than five minutes, let rollback stop it, fix the problem, and re-arm for another
supervised start. Do not enable boot startup or cancel the rollback until step 5 passes.
Never run the strict free-port gate against an already running DNS service.

## 5. Install HTTPS and verify independent DNS — Pi and wired client

From the recovery PC: `ssh -L 15380:127.0.0.1:5380 adam@rasp-pi` (use the verified address
if the old alias no longer resolves). Browse `http://127.0.0.1:15380`, sign in as `admin`
with the private password, and verify **no DHCP scopes**. Follow the management HTTPS
guide to transfer only the Pi's issued PFX, never the CA root key. Install it privately
as `/var/lib/technitium-pi/tls/management.pfx`, owned by `technitium-pi`, mode 0600; the
tls directory must be mode 0700. Set that path and its password in Settings / Web Service,
enable HTTPS on 53443, disable self-signed TLS, and add the verified Pi LAN and approved
VPN address alongside loopback. The firewall continues to deny non-loopback 5380. Verify
direct trusted HTTPS before enabling HTTP-to-TLS redirect. Do not put TLS passwords in
the environment, scripts or command arguments.

In Settings verify `UseSpecifiedNetworkACL` and exact authorized client ranges, no proxy,
**no forwarders**, and DNSSEC validation enabled. Create only a temporary Primary zone
`deployment-test.home` with A record `probe` pointing to the Pi's private address. The
permanent `home`/reverse zones remain #105. Use the wired recovery client:

```bash
dig @"$PI_ADDRESS" probe.deployment-test.home A
dig +tcp @"$PI_ADDRESS" probe.deployment-test.home A
dig @"$PI_ADDRESS" "$FRESH_EXTERNAL_NAME" A
python3 services/network/ca/certificates.py probe --name raspberrypi.home \
  --address "$PI_ADDRESS" --ca-cert "$ROOT_CERT"
```

Verify the exact local answer, successful recursive external answer, trusted chain/name,
expiry and browser trust without exception. Clear the Pi cache and resolve an uncached
name with its peer unavailable **to the Pi only**, using a reviewed temporary outbound
firewall restriction on the Pi for the peer's DNS endpoints. Do not stop or change homelab.
The no-forwarder settings plus packet capture on the Pi must show direct external DNS
queries, with no peer requests. Record and remove only that temporary restriction afterward.
Check a signed domain validates and a deliberately invalid signed domain gives SERVFAIL.

From separate authorized and unauthorized clients verify DNS and HTTPS access/denial,
including IPv4/IPv6 and non-loopback HTTP denial. Record firewall counters. Verify no
UDP 67 listener with `sudo ss -H -lunp`, zero DHCP scopes and no Technitium offers during
a wired client's Bell renewal using the preflight capture procedure. Bell alone supplies
the same gateway/DNS options. Repeat all baseline homelab DNS/household service checks.
Delete only the temporary test zone after these probes.

Cancel the service rollback after these checks, enable the service, and reboot supervised:

```bash
sudo systemctl stop raspberrypi-dns-service-rollback.timer
sudo systemctl enable technitium-pi.service
sudo reboot
```

Recheck firewall-before-DNS, trusted HTTPS, DNS, no scopes/UDP 67 and original services.
The service's `Requires`/`After` loads the firewall on each boot; a firewall load failure
must keep DNS stopped. Test service stop/restart and the removal path before acceptance.

## 6. Pi capacity, power and storage — supervised pilot and soak

The #99 figures are observations, not agreed numeric limits: cold recursion 1419 ms,
800 cached queries/8 workers p95 9.2 ms, filtered p95 8.8 ms and about 120 MiB RSS. Review
these conservative **proposed deployment guardrails** as part of the exact plan: cold
recursion under 5 seconds, cached and filtered p95 under 100 ms at the same workload,
service RSS under 256 MiB, at least 256 MiB available Host memory and 1 GiB free storage,
no sustained CPU saturation at ordinary load, `get_throttled=0x0`, and no SD/I/O errors.
Require a 24-hour ordinary-load soak with no crashes, power warnings or increasing memory/
storage consumption before completing #104. Agree any different limits before testing.
This soak does not establish Pi cluster membership, which remains #106.

On the Pi, clear cache through the console, select a resolving `EXTERNAL_TEST_NAME`,
and temporarily block a separate reviewed `BLOCKED_TEST_NAME` with blocking type NXDOMAIN.
Run this client-only workload against loopback; importing the helper starts no service
and sends no API mutation. Do not invoke the pilot launchers or `pi_probe.py` main routine:

```bash
PYTHONPATH=services/network/pilot python3 - "$EXTERNAL_TEST_NAME" "$BLOCKED_TEST_NAME" <<'PYTHON'
from concurrent.futures import ThreadPoolExecutor
import sys
from pi_probe import query
cold = query(sys.argv[1])
assert cold[0] == 0 and cold[1] and cold[2] < 5000, 'cold recursion failed'
print(f'cold_ms={cold[2]:.1f}')
for name, code, label in ((sys.argv[1], 0, 'cached'), (sys.argv[2], 3, 'filtered')):
    with ThreadPoolExecutor(max_workers=8) as workers:
        results = list(workers.map(lambda _: query(name), range(800)))
    errors = sum(result[0] != code or bool(result[1]) != (code == 0) for result in results)
    p95 = sorted(result[2] for result in results)[759]
    print(f'{label}: queries=800 workers=8 errors={errors} p95_ms={p95:.1f}')
    assert errors == 0 and p95 < 100, 'workload guardrail failed'
PYTHON
```

Remove only the temporary blocked test domain after the probe. Repeat actual LAN/VPN
queries separately; loopback timings do not establish client access. Use
`sudo systemctl show technitium-pi -p MainPID -p MemoryCurrent -p CPUUsageNSec -p NRestarts`,
`ps` for RSS/CPU, `free -m`, `df -h /`, `vcgencmd get_throttled` and
`sudo journalctl -k --since ...` at start/end and during the soak. Inspect private service
logs for failures. Logging retention is seven days; in-memory statistics limit SD writes.
Do not silently accept power, latency, CPU or storage regressions; stop DNS and recover.

## 7. Service removal and Host rollback — Pi console

Rehearse service stop/restart first. Before removing, take a protected backup following
the backup guide. Removal stops/disables only this service, removes its binaries/units
and deletes only its dedicated firewall table. It preserves credentials, configuration
and owning service account so private recovery data stays available:

```bash
sudo bash "$BUNDLE_DIR/remove.sh"
sudo ss -H -lntu
```

Verify DNS/management/UDP 67 listeners are absent, homelab still answers, Bell remains the
sole DHCP source, and the Pi still has gateway/internet/SSH. For a partial installation,
inspect which units exist before running the same individual removal commands; do not
mask a failed stop or remove firewall protection while DNS is still running. To reinstall,
move preserved private directories into the recovery packet and remove the unused service
account/group only after confirming it owns no running process or other data. A clean
installer deliberately refuses these remnants; never point it at old or restored state.

For address rollback, run `$PACKET_DIR/rollback-pi-address.sh` locally, verify the original
profile and recovery client access, then delete only `raspberrypi-dns-static`. If needed
restore the saved NetworkManager profiles as described in step 2. Preserve Bell's gateway,
subnet and DHCP configuration; return any manually configured test client to its prior DNS.
Reinstall/verify using fresh state and the same reviewed plan after the removal rehearsal,
then repeat reboot and soak before accepting. Do not delete the only good backup.

Retain privately: operator/date and commit/pins, exact approved plans, profile/rollback
and reboot results, permitted/denied access probes, independent recursion/DNSSEC, trusted
HTTPS, absence of scopes/listeners/offers, capacity/24-hour soak, and removal/reinstall
results. Publish only a redacted pass/fail summary after every acceptance row passes.
