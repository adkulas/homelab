# Home Site setup and recovery walkthrough

Use this guide from a recovery PC with a second operator before changing the household
network. Bell remains the gateway on its existing subnet throughout. Work on one Host
at a time; keep the other resolver running. Only `homelab-dns` may eventually serve DHCP.
The Pi always has DHCP disabled. Record results in the private recovery packet, including
the date, operator, completed step, verification and rollback outcome.

Commands below run from the repository root unless stated otherwise. All path variables
are operator-selected absolute paths outside Git on encrypted storage. Address variables
are read privately from the validated Site Inventory, never copied into this guide,
issues, shell history or shared logs. Disable shell tracing; use `umask 077`. Read tool
output locally; never attach decrypted inventory, route output, credentials or captures.

## Availability and acceptance checks

This guide is the #102 preparation deliverable. Production deployment is gated on later
tickets. A gate means **stop at that step** until the named ticket supplies its reviewed
artifact and operator procedure; it does not authorize substituting pilot scripts.
The pilot Compose file and Pi namespace runner are disposable test tools, not installers.

| Operator boundary | Required behavior and source | Evidence to retain privately |
| --- | --- | --- |
| Inventory and trust | #97, #98, #100; ADRs 0004–0008 | Redacted validation/preflight, tested console, root fingerprint and HTTPS probes |
| Host and DNS preparation | #103–#107; home network plan | One-Host rollback, disabled scopes, independent DNS, exact protected plans, zone age |
| DHCP and operation | #108–#110; home network plan | One offer source, client options, applied commit, failure and upgrade drill results |
| Recovery PC and detached Passport | #101; backup/restore CLI | Verified destination copies, isolated drill, recoverable identity without homelab |

The [network plan](../../../docs/plans/home-network.md) records the design;
[the topology guide](../../../sites/home/docs/topology.md) describes the encrypted inputs.
This walkthrough orders the existing [preflight](preflight.md), [CA](management-https.md)
and [backup/recovery](backup-restore.md) procedures. Their complete commands and stop
conditions apply whenever a step delegates to them.

## 1. Establish recovery access — PC and Bell admin page

**Prerequisites:** Administrator access to Bell, encrypted PC storage, Ethernet recovery
client and physical console access to both Hosts. Choose `PACKET_DIR` outside Git.

**Action:** Copy `services/network/docs/recovery-packet.template.md` into that directory.
Follow preflight sections 1–2: privately save Bell's current LAN/DHCP/IPv6 settings,
leases, exact DHCP disable/re-enable controls, Host profiles and routes. From the wired
client, open Bell's local admin page and reach each available Host by its privately
recorded address with DNS unavailable. Test the local keyboard/display or console.
Record where the packet and spare identity can be found without homelab.

**Expected/verify:** A second operator can locate the packet, log in locally, and restore
Bell's prior DHCP settings. Inspect fixed devices and current leases as well as the
historical encrypted snapshot; a silent ping does not prove address availability.

**Stop:** Missing credentials, untested console, conflicting addresses, unexplained IPv6
DNS advertisements or unknown DHCP responders. **Rollback:** Read-only; no network change.

## 2. Unlock and validate Site Inventory — administrator PC

**Prerequisites:** Go, SOPS, age and the existing Site age identity from private custody.
`SOPS_AGE_KEY_FILE` names that private identity file; it must remain outside Git.
The current topology has one recipient with copies on three machines, not three keys.
The backup recovery identity and offline CA recovery identity have separate custody.

**Action:** Set `SOPS_AGE_KEY_FILE` privately, then run:

```bash
go run ./cmd/homelab site validate --site home --output json
```

If a correction is needed, edit ciphertext through `sops sites/home/secrets/site.sops.yaml`
or `sops sites/home/secrets/devices.sops.yaml`, validate again, and commit only ciphertext.
Use an editor without plaintext swap/backup files. Preserve the previous ciphertext.

**Expected/verify:** Exit 0 and a valid redacted `networkPlan`. Compare semantic Host,
reservation and DHCP relationships with the private Bell packet. Validation does not
check the live LAN. **Stop:** Decryption/schema failure or unconfirmed device identity.
**Rollback:** Restore the previous encrypted document and validate; no service was changed.

## 3. Prepare the Pi OS — PC, then raspberrypi local console

**Prerequisites:** Saved Pi SD-card image if it contains anything needed, verified Pi 3
power supply, spare SD card, wired console and the recovery packet. The measured pilot
used Debian 13 ARM64 and .NET 10.0.13; the earlier plan's 32-bit Raspberry Pi OS suggestion
is not evidence for a 32-bit production runtime. #104 must settle and document the OS,
architecture, native release checksum, service unit and longer capacity soak first.

**Action (gate #104):** On the PC, use Raspberry Pi Imager to choose the OS/architecture
approved by #104, set a unique administrator account and SSH public key, select the
identified spare SD card, write and verify it. Boot the Pi on Ethernet, log in at its
local console, and privately record the OS release, architecture and active network
profile. Keep DHCP automatic at this step; install no DHCP service. Preserve the original
SD card for rollback. Use the [Pi procedure](secondary-dns.md) for OS updates and reboot before proceeding.

**Expected/verify:** Local and SSH login work; OS/architecture match the reviewed native
runtime and there are no power/storage errors. Run the Pi preflight in step 4.
**Stop:** Wrong storage target, unsupported architecture, unstable power or no console.
**Rollback:** Shut down locally and boot the preserved prior card; Bell is unchanged.

## 4. Complete read-only preflight — each named machine

**Prerequisites:** Steps 1–3 for the Pi; step 1 for other machines. Python 3 and the tools
listed by the preflight. `EVIDENCE` names that machine's private boolean evidence file.

**Action:** Run the matching command on each machine, not all three on the PC:

```bash
python3 services/network/preflight.py --role homelab --evidence "$EVIDENCE"
python3 services/network/preflight.py --role raspberrypi --evidence "$EVIDENCE"
python3 services/network/preflight.py --role recovery-client --evidence "$EVIDENCE"
```

Initially omit `--evidence` to discover blockers. Complete every manual confirmation in
the [preflight guide](preflight.md), then set only proven booleans and rerun. Investigate
DNS TCP/UDP 53, DHCP UDP 67, management ports, routes, Docker bindings and IPv6 behavior.

**Expected/verify:** All three exit 0; dated private evidence explains every confirmation.
**Stop:** Any blocker or unexplained listener. **Rollback:** Read-only; retain evidence.

## 5. Prepare HTTPS and recovery custody — offline signer and PC

**Prerequisites:** Step 2, separate offline CA custody and independent backup storage.
**Action:** Follow the [CA guide](management-https.md) in order: create the encrypted root
on the offline computer, back it up separately and rehearse recovery; generate distinct
`homelab.home` and `raspberrypi.home` requests on the PC; transfer only CSRs for signing;
return public certificates, package/encrypt each leaf and password, and authenticate the
root fingerprint before installing trust on administrator devices. Keep the root off
both DNS Hosts. Prepare the recovery PC tools, image and identities from step 12 now.

**Expected/verify:** Both leaf files pass `certificates.py verify`; the independent root
backup can sign a disposable request. **Stop:** Fingerprint/name mismatch, lost custody or
failed decryption. **Rollback:** Remove unused issued material and newly installed trust
as described in the CA guide. Do not replace an established root without its rotation plan.

## 6. Assign static Host addresses — one local Host console at a time

**Interruption:** Address changes can disconnect SSH and services.
**Prerequisites:** Fresh step-4 results, private original profiles, confirmed free addresses
outside Bell allocation, wired recovery access and reviewed Host-specific profiles.
For homelab, follow the [primary DNS procedure](primary-dns.md), including its private
Host profile review and timed reversion. For the Pi, follow the [native DNS procedure](secondary-dns.md), including inactive
profile staging, exact-plan approval and timed NetworkManager reversion.

**Action:** On homelab, stage the reviewed private Netplan profile preserving Wi-Fi and
route priorities, then run `sudo netplan try` locally. Confirm only after checking
connectivity from the recovery client. On Pi, follow #104's timed NetworkManager change
procedure at its console using the recorded connection name. Change one Host, verify,
then move to the next; Bell's gateway/subnet stay unchanged.

**Expected/verify:** After each change and reboot, check gateway/internet access, SSH,
Docker, Traefik, Media Stack, Minecraft forwarding, cameras/NVR and Tailscale where used.
Re-run that Host's preflight and privately compare routes with the saved originals.
**Stop:** A lost route, service regression or address conflict. **Rollback:** Let the timed
change revert. Otherwise restore original Netplan files and `sudo netplan apply` locally;
on Pi restore the saved profile, `sudo nmcli connection reload`, then activate the saved
connection from the console. Recheck access before trying again.

## 7. Start DNS only and install HTTPS — homelab, then raspberrypi

**Prerequisites:** Steps 5–6; #103's reviewed host-networked Compose and #104's pinned
native systemd installer/removal procedure. Homelab's Compose and supervised procedure
are available in [primary DNS](primary-dns.md); Pi artifacts and commands are in
[native Pi DNS](secondary-dns.md). Live acceptance remains required for each Host.

**Action:** Follow each deployment ticket's install/start commands
on its assigned Host. Homelab uses the digest in `services/network/technitium.env`, private
persistent configuration and no Compose published ports or Docker socket. Pi uses the
reviewed native version/runtime under systemd. Do not run the pilot as a live service.
Create no DHCP scope. Through the CA guide's SSH-tunneled initial console, replace default
administrator credentials, install that Host's leaf and enable direct HTTPS on 53443.
Apply reviewed LAN/VPN recursion and management restrictions on IPv4 and IPv6; leave no
WAN port forwarding. Perform this sequence on one Host and verify before starting its peer.

**Expected/verify:** Query an uncached external name directly against each Host with
`dig @"$RESOLVER_ADDRESS" "$EXTERNAL_TEST_NAME" A`; inspect the answer and recursion flags.
Choose a known resolvable external test name not already queried. Confirm independent
recursion, DNSSEC policy, no enabled scopes and no DHCP offers. Use the CA probes:

```bash
python3 services/network/ca/certificates.py probe --name homelab.home --ca-cert "$ROOT_CERT"
python3 services/network/ca/certificates.py probe --name raspberrypi.home --ca-cert "$ROOT_CERT"
```

Before local DNS exists, use each probe's `--address` option with its verified private
address. Verify browser trust without exceptions and permitted/denied network paths.
**Stop:** TLS failure, open recursion, unexpected DHCP, Pi resource/power failure or any
existing service regression. **Rollback:** Stop/remove only the new selected service via
its deployment procedure; preserve private configuration, restore its prior leaf/settings
if appropriate, return the test client to its previous DNS, and use step 6 for addressing.

## 8. Declare local DNS and join the DNS Cluster — administrator PC

**Prerequisites:** Both HTTPS checks, a verified paired backup (step 12), and #105's exact
plan workflow plus #106's reviewed cluster join/promotion procedure. Bell still owns DHCP.

**Action (gates #105/#106):** Use #105's read/diff path to review `home`, reverse zones,
fixed Host A/PTR records and service aliases against the Site declarations. Run the backup
gate from step 12 before protected apply. Apply the exact current plan; reject a stale
plan. Report unknown records for operator review. Use #106's supported console procedure
to initialize the primary and join the Pi over trusted direct HTTPS using `cluster.home`;
include local/reverse zones in the catalog and synchronize filtering. Configure SOA expiry
beyond 72 hours with margin, and record the measured transfer deadline and promotion path.

**Expected/verify:** Repeat plan is empty. On each resolver run the step-11 DNS checks;
compare local answers, PTRs, filtering, serials and transfer age. Rehearse either service
stopping, one at a time, including Pi promotion before secondary expiry. Record results.
**Stop:** Divergence, certificate bypass, failed transfer or collision protection test.
**Rollback:** Pause applies and reverse the selected exact change or recover that one node
via step 12. Keep the other resolver available and Bell DHCP enabled. Never restart both.

## 9. Stage DHCP without household allocation — homelab

**Prerequisites:** Step 8, fresh backup gate, isolated test path and #107's exact-plan scope
staging procedure. **Gate #107:** Scope creation in the pilot enabled allocation immediately;
creating a household-connected scope and disabling it afterward is unsafe.

**Action:** Follow #107's isolated stage/apply workflow. Review scope bounds/excluded
reservation band, MAC reservations, retained Bell gateway, both resolver options and
`home` suffix against the encrypted topology. Use explicit two-resolver options with
`useThisDnsServer` false. Verify allocation disabled before connecting household broadcasts.

**Expected/verify:** Repeated plan is empty; an isolated dynamic and reserved client gets
correct options and A/PTR updates on both DNS Hosts; protected scope changes cannot auto
apply. Pi has no allocating scope. Confirm no Technitium offers reach the household LAN.
**Stop:** Enabled scope on an exposed interface, wrong options or declared-name overwrite.
**Rollback:** Isolate/stop Technitium DHCP immediately, restore the prior disabled scope
via the reviewed plan, and verify Bell remains the sole household offer source.

## 10. Supervised DHCP handoff — Bell page, homelab console and wired client

**Interruption:** New and renewing clients can lose connectivity.
**Prerequisites:** #108 supervised handoff procedure, steps 1–9 verified, fresh paired
backup/gate, Bell rollback record, wired/local access and representative test clients.

**Action (gate #108):** Open Bell's exact DHCP page recorded in the packet. Disable Bell
DHCP and save; verify Bell no longer offers. Only then enable the prepared Technitium
scope on homelab using #108's procedure. Renew a wired test client's lease using its OS
network control. Inspect an offer capture/server identifier through #108's documented
probe, not just the selected lease. Verify exactly one source before renewing other
representative wired/Wi-Fi, dynamic and reserved clients. Keep Bell's LAN settings intact.

**Expected/verify:** Step 11 passes, including both DNS Hosts' fixed and lease names.
**Stop:** Two/zero sources, incorrect options, inaccessible devices or missing names.
**Rollback:** Disable Technitium DHCP first and verify no offers. Restore/re-enable Bell
DHCP with the packet's prior settings, renew the wired test client, verify Bell alone,
gateway and DNS, then recover other clients. Never enable both responders together.

## 11. Verify service and enable routine polling — PC, clients and homelab

**Prerequisites:** Successful handoff. Use `RESOLVER_ADDRESS` separately for each privately
verified DNS address and `PTR_ADDRESS` for the declared Host's private address.

**Action:** From the PC, repeat for each resolver:

```bash
dig @"$RESOLVER_ADDRESS" homelab.home A
dig @"$RESOLVER_ADDRESS" jellyfin.home CNAME
dig @"$RESOLVER_ADDRESS" -x "$PTR_ADDRESS"
dig @"$RESOLVER_ADDRESS" "$FILTERED_TEST_NAME" A
dig @"$RESOLVER_ADDRESS" "$EXTERNAL_TEST_NAME" A
```

Select filtered/external names and their expected outcomes from the reviewed policy;
use a fresh external name per independent recursion check. On clients inspect the lease
address/band, gateway, two DNS endpoints, suffix, reservation and DHCP server. Query
fixed and lease-derived names on both Hosts. Check Docker's resolver path and household
services as in step 6. Inspect IPv6/DoH exceptions; DHCP DNS options do not enforce filtering.
Record cluster transfer age against the 72-hour target, Pi resources and backup age.

**Action (gate #109):** Only after manual exact apply and recovery pass, install/start the
reviewed #109 systemd units with its documented command. There is currently no production
polling unit or `network-stack` command. Verify locking, an empty no-op without restarts,
an alias applied once, protected/mixed commits applying nothing, and invalid/decryption/
Git/API failures retaining working services. Use #109's status view for the active applied
commit; `git rev-parse HEAD` proves only checkout identity. Complete #110 health/failure
drills before claiming monitoring or recovery readiness.

**Expected/verify:** Client service checks pass and deployment evidence matches the active
commit. Capture a new paired backup after cutover. **Stop:** Any regression or missing
deployment evidence. **Rollback:** Stop/disable only #109's timer using its documented unit
name, return through the reviewed exact plan, or use step 10 for DHCP rollback. A Git
revert alone does not prove live state reverted.

## 12. Worked backup, PC/Passport copy and outage recovery — recovery PC

**Prerequisites:** Use [backup-restore.md](backup-restore.md) to prepare Python 3, age,
Docker, cached pinned image, private two-node `BACKUP_CONFIG`, `IDENTITY`, trusted CA and
checkout on the PC. Keep keys separately recoverable. Set all paths below outside Git:
`BACKUP` to a fresh PC `.age` file, `COPY_DIR` to the PC archive directory, and
`PASSPORT_COPY` to a fresh filename on the actual mounted encrypted Passport filesystem.
Confirm its mounted identity/capacity first; never copy into an absent drive's mountpoint.

**Action while both nodes work:** Pause configuration changes; capture, copy and verify:

```bash
python3 services/network/backup/backup.py create --config "$BACKUP_CONFIG" --output "$BACKUP" --identity "$IDENTITY"
python3 services/network/backup/backup.py list --directory "$COPY_DIR"
python3 services/network/backup/backup.py verify --archive "$BACKUP" --identity "$IDENTITY"
cp -n -- "$BACKUP" "$PASSPORT_COPY"
python3 services/network/backup/backup.py verify --archive "$PASSPORT_COPY" --identity "$IDENTITY"
python3 services/network/backup/backup.py gate --archive "$BACKUP" --identity "$IDENTITY" --max-age-hours 24
```

**Expected/verify:** Both selected files verify; gate exits 0 before protected changes.
Record locations/date/node versions privately. Safely unmount, detach and store Passport
separately. Retain an older verified copy. A PC that cannot reliably retain backups does
not replace detached media. **Stop:** Missing mount, failed capture/copy/decryption or
stale gate. **Rollback:** Remove only incomplete new copies; retain known-good archives.

**Action for a drill, or when homelab is unavailable:** On the recovery PC locate the
packet, checkout, cached image and independent identity. Attach Passport if needed;
list that directory, select `BACKUP` and verify it again. No source API or homelab is
needed. Select fresh private `RECOVERED` and `RESTORE_PLAN` paths and run:

```bash
python3 services/network/backup/backup.py restore-plan --archive "$BACKUP" --identity "$IDENTITY" --node homelab --mode drill --output "$RECOVERED" --plan "$RESTORE_PLAN"
python3 services/network/backup/backup.py restore --plan "$RESTORE_PLAN" --identity "$IDENTITY"
```

Review the exact archive/node/image/output and `dhcp: disabled`; preview must report
`confirmation-required` without creating the sandbox. Then apply:

```bash
python3 services/network/backup/backup.py restore --plan "$RESTORE_PLAN" --identity "$IDENTITY" --confirm
```

**Expected/verify:** `recovered-stopped`, mode `drill`, DHCP disabled, matching zone/scope
counts. The tool uses an internal Docker bridge, no published ports and disabled serialized
scopes before import and after restart. Never bridge it to the household LAN or reuse its
drill settings as a live node. Repeat for `--node raspberrypi` with fresh paths.
**Stop:** Isolation, integrity or disabled-scope check failure; discard partial output.
**Rollback:** Remove only the disposable recovered directory after inspection.

For actual recovery, follow **every** supervised live recovery step in
[backup-restore.md](backup-restore.md#supervised-live-recovery). Fence the failed Host at
power/switch level; identify the current DHCP owner; preview a fresh `--mode live` plan
and confirm only the intended node. The output is a stopped, private configuration export,
not a started replacement. Preserve the target's prior configuration, install the recovered
configuration with the pinned version and correct ownership, restore external leaf paths,
then validate under isolation before reconnecting DNS. An outage backup may be older than
24 hours; that does not waive the freshness gate for a new planned protected change.
Restore the peer separately. Enable DHCP only by step 10, with exactly one responder.
If validation fails, stop/isolate the replacement and restore its prior stopped directory;
use the Bell rollback if allocation must be recovered. Never boot the fenced old DHCP Host
alongside the replacement. Back up the CA root through its separate offline procedure.

## 13. Rehearse failure and upgrades — supervised maintenance

**Prerequisites:** Verified recovery copies, step 11 stable, #110 drill procedure and
measured zone-transfer deadline. **Gate #110:** Record actual results, not assumed passes.

**Action:** Exercise each Host failing separately, internet loss, Git loss, API failure,
cluster-sync loss, DHCP loss and bad configuration/release. Check local DNS, fresh external
answers where internet is available, lease behavior, alerts and restoration after each.
Promote Pi using #106's supported procedure before the 72-hour secondary service margin
is exhausted; expiry can remove local authority while external recursion still works.
For upgrades take a paired backup/gate, review the protected version plan and retain the
previous release/configuration. Upgrade Pi first, verify independent DNS, HTTPS, replication,
filtering and capacity, then upgrade homelab and verify again. Never restart both together.

**Expected/verify:** Record outage duration, surviving service, zone age/deadline, recovery
steps/results and post-recovery client checks privately. **Stop:** Failed check or inadequate
secondary margin. **Rollback:** Pause polling/applies, restore the selected Host's previous
pinned release with its compatible known-good configuration, verify it before touching the
peer; use step 12 for recovery and step 10 for DHCP fallback. Do not downgrade an already
migrated configuration in place without the prior compatible backup.
