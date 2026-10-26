# Paired DNS backup and recovery

Issue #101 provides `python3 services/network/backup/backup.py`. Run it from the
repository root on an administrator PC with Python 3, age, and age-keygen. Recovery
also requires Docker Engine with a local Linux bridge and the pinned Technitium
image. It works without the homelab Host: keep a checkout, the image, recovery
identity, CA trust, and this guide on the recovery PC before an outage. Native Pi
recovery uses the exported configuration directory with the same Technitium version.

## Behavior and evidence

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| `create` | Capture both DNS Stack Instances together; encrypt before writing data | #101; pinned v15.6.2 backup API | Both node ZIPs, checksums, version and repository commit in encrypted manifest; real-age TLS command tests |
| `list`, `verify` | Find copies and prove decryptability and archive integrity | #101; recovery identity outside Git | Verify the actual selected PC/Passport file; corruption and wrong-identity tests |
| `restore-plan`, `restore` | Read-only preview; exact backup; isolated recovery; DHCP disabled | #101; ADR-0007 | Changed backup rejected; Docker rehearsal checks recovered zone and full node settings, restart confirms scopes disabled |
| `gate` | Missing, failed, corrupt, undecryptable or stale backup blocks protected changes | #101; ADR-0007 | Nonzero exit status; no successful evidence file from failed `create` |

The Site owns topology; each DNS Stack Instance owns its runtime configuration.
The backup is a recovery artifact, including authentication secrets, cluster keys,
certificates, scopes, reservations, lease state present in upstream scope files,
zones, DNS settings, filtering policy and apps. It is encrypted outside Git. The
manifest is encrypted too; normal reports print no topology, credentials or ZIP
contents. Runtime statistics and query logs are intentionally excluded. An empty
scope set or uninitialized DNS Cluster is valid; initialized nodes must include
`cluster.config`, and existing scopes/zones must have corresponding backup files.

The shared Docker pin is `services/network/technitium.env`; recovery derives its
version from the same image used by Compose. The scope adapter is deliberately
limited to the pinned format and must be reverified on upgrades. Its layout is
defined in upstream [Scope.cs](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/Dhcp/Scope.cs).

The pinned API flags and file names come from upstream
[backup/restore implementation](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/DnsWebService.cs)
and [settings API](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/WebServiceSettingsApi.cs).
Technitium flushes some files asynchronously. An incomplete capture fails; wait for
the service to flush and retry rather than accepting a smaller archive. Captures
are sequential snapshots, so perform them while configuration changes are paused.

## Recovery identity and credentials

On the recovery PC, choose private paths outside every Git checkout. In the commands
below, set `RECOVERY_DIR`, `COPY_DIR`, `BACKUP`, `BACKUP_CONFIG`, `IDENTITY`,
`RESTORE_PLAN`, and `RECOVERED` to your selected absolute private paths. Do not paste
resolved operational values or command output into GitHub.

```bash
umask 077
mkdir -p "$RECOVERY_DIR"
age-keygen -o "$RECOVERY_DIR/recovery.key"
age-keygen -y "$RECOVERY_DIR/recovery.key"
```

Keep the identity off Git and off the routine deployment account. Store another
protected copy separately from the encrypted backups. Confirm you can recover it
when homelab is unavailable. An offline CA root backup remains a separate artifact
under the [CA guide](management-https.md); this backup must never contain that root.

Create a separate Technitium backup identity/token on **each** DNS Host. It needs
`Settings: View` and `Settings: Delete` for settings and backup, `Admin: View` for
DNS Cluster observation, `Zones: View`, and `DhcpServer: View`. `Settings: Delete` is
broad: never grant it to the routine Git reconciler. Store these tokens in separate
mode-0600 files, available only to the recovery operator. The restore sandbox uses
fresh temporary administrator credentials and never sends a source token to it.

Create the mode-0600 JSON configuration outside Git using this structure. Replace
the semantic placeholders privately; no plaintext operational addresses belong in
this repository. Both nodes are mandatory. `caFile` is the trusted management CA;
omit it only when system trust already validates the endpoint. HTTPS hostname and
chain validation are always enabled for capture. Redirects and proxies are disabled.

```json
{
  "recipients": ["AGE_RECOVERY_RECIPIENT"],
  "nodes": {
    "homelab": {
      "url": "HTTPS_ORIGIN_FOR_HOMELAB_MANAGEMENT",
      "tokenFile": "ABSOLUTE_PRIVATE_HOMELAB_BACKUP_TOKEN_FILE",
      "caFile": "ABSOLUTE_CA_CERTIFICATE_FILE"
    },
    "raspberrypi": {
      "url": "HTTPS_ORIGIN_FOR_RASPBERRYPI_MANAGEMENT",
      "tokenFile": "ABSOLUTE_PRIVATE_PI_BACKUP_TOKEN_FILE",
      "caFile": "ABSOLUTE_CA_CERTIFICATE_FILE"
    }
  }
}
```

## Capture and copy

On the PC, attach/mount the selected destination first. For Passport, confirm the
actual removable filesystem is mounted; do not write to a bare mountpoint when the
drive is absent. Choose a fresh timestamped `.age` filename on that filesystem.
Existing files are never overwritten. Keep the identity on separate protected media.

```bash
python3 services/network/backup/backup.py create \
  --config "$BACKUP_CONFIG" --output "$BACKUP" --identity "$IDENTITY"
python3 services/network/backup/backup.py list --directory "$COPY_DIR"
python3 services/network/backup/backup.py verify --archive "$BACKUP" --identity "$IDENTITY"
```

Expected result: `status: verified`. `create` requests every recovery component,
validates ZIPs and required files, encrypts, decrypts the new copy, verifies checksums,
and only then publishes the destination. Any failed node fails the entire capture;
there is no partial paired backup. Copy the encrypted `.age` file to the other
selected destination and run `verify` against **that copy**. Listing is filename
discovery only; it does not claim copies are verified. Safely unmount and detach
Passport after verification. Keep an older verified copy when making a new one;
there is no automatic retention or deletion.

Before a protected change, the supervised driver must run:

```bash
python3 services/network/backup/backup.py gate \
  --archive "$BACKUP" --identity "$IDENTITY" --max-age-hours 24
```

Proceed only on exit 0. This decrypts and verifies both nodes and rejects stale or
future-dated evidence. A filename, prior success report, or failed new capture is
not approval to continue. Future exact-plan DHCP/DNS/upgrade commands must invoke
this gate immediately before mutation; those commands belong to later tickets.
Routine polling must not have the recovery identity, backup tokens, or restore
confirmation. Errors return nonzero with a redacted message and require stopping.

## Restore Drill

On the recovery PC, choose fresh paths for `RESTORE_PLAN` and `RECOVERED` outside
Git. Ensure enough local storage for decrypted recovery state. Treat that directory
as secret. The operator command creates its own fresh internal Docker bridge with
no published ports and a fresh anonymous configuration volume. It never accepts an
existing container or household LAN target. Before import, the tool clears only the enabled flag in each serialized scope using
the pinned v10 format; reservations, options and leases remain intact. Unsupported
formats fail closed. DHCP stays disabled throughout import and restart.

```bash
python3 services/network/backup/backup.py restore-plan \
  --archive "$BACKUP" --identity "$IDENTITY" --node homelab \
  --mode drill --output "$RECOVERED" --plan "$RESTORE_PLAN"
python3 services/network/backup/backup.py restore --plan "$RESTORE_PLAN" --identity "$IDENTITY"
```

Inspect the private plan: correct archive, node, output, pinned image, mode and
`dhcp: disabled`. Expected preview: `confirmation-required`, with no output directory
and no Docker changes. Apply that exact plan:

```bash
python3 services/network/backup/backup.py restore \
  --plan "$RESTORE_PLAN" --identity "$IDENTITY" --confirm
```

Expected result: `recovered-stopped`, mode `drill`, `dhcp: disabled`. Zone and scope
counts must match the archive; every scope is checked disabled immediately after import and after restart
inside the isolated network. Only zones/scopes are restored into the running drill;
production authentication, cluster connections and listening addresses are excluded.
The stopped drill directory retains disposable sandbox settings. Do not adopt it as
a complete live node. Remove it privately when finished. Repeat for `raspberrypi`
using fresh plan and output paths. A changed/corrupt archive, existing output,
isolation failure, or restore failure stops the operation; do not use partial state.

## Supervised live recovery

1. On the recovery PC, verify the selected backup even if homelab is unavailable.
   Older backups are allowed for recovery; the freshness gate is for new protected
   changes. Record the outage and intended single-node recovery privately.
2. Stop the failed DNS Stack Instance, disable its auto-restart, and disconnect its
   network access. If homelab is unavailable, fence it at power or switch level so
   it cannot return as a DHCP responder. Establish wired/local access. Identify
   the current DHCP owner (Bell or Technitium); do not change Bell addressing.
3. Run the same planning/preview/apply commands with `--mode live` and the affected
   `--node`. Review the plan before `--confirm`. The tool disables serialized scopes before restoring
   them in isolation, stops the sandbox, then overlays the original auth, DNS,
   cluster, web settings, filtering, apps and certificates into the stopped output.
   It never overlays the original enabled scope files. This directory contains the
   original node's identity and must only replace that node, never a second node.
4. Keep the replacement service stopped and disconnected. Take a private copy of
   its prior configuration for rollback. Copy the recovered directory to its
   Technitium configuration directory (Docker `/etc/dns`, or the Pi's configured
   native configuration directory); preserve private permissions and set ownership
   to the service account. Use the exact pinned version. Restore original mount
   paths for certificates/apps. Verify external certificate files separately if
   they were outside Technitium's configuration directory and absent from its ZIP.
5. Start that one replacement under local isolation. Verify original authentication,
   HTTPS trust, DNS settings, representative local records, cluster identity and
   reservations against the private recovery record. Verify all scopes remain
   disabled. Stop if settings differ or any scope allocates unexpectedly; stop the
   replacement and return to the prior stopped configuration.
6. Reconnect DNS service only after those checks. Recover/rejoin the other node
   separately; never run two copies of a restored cluster identity. Re-enable DHCP
   only through the supervised #108 handoff: exactly one active source, Bell
   disabled first when handing off to Technitium, one test-client offer, then verify
   addresses/options/local DNS. Roll back DHCP by disabling Technitium **before**
   re-enabling Bell and renewing a test client. This command never enables DHCP or
   starts a production service. Keep the fenced old Host stopped until reviewed.

## Developer verification

Run `go test ./tests/acceptance -run '^TestNetworkBackupCommands$'` for command tests
using a simulated TLS API and real age encryption, or the underlying Python suite:

```bash
python3 -B -m unittest discover -s services/network/backup -p 'test_*.py'
services/network/pilot/backup_check.sh
```

The Docker rehearsal uses fresh pinned pilot nodes, an initialized two-node DNS
Cluster, an A record and an enabled disposable scope with a reservation. It verifies
both encrypted destination copies, the gate, dry runs, drill recovery and complete
stopped recovery of each node. It checks zone bytes, unchanged reservation/options/lease bytes after the scope
enabled flag, and original node configuration;
recovery itself verifies disabled DHCP across restart. The pilot has no published
Host ports and shares the existing pilot lock. It does not test physical Passport
media, household leases, or actual Pi/production recovery. Record those operator
checks privately during the later supervised deployment and cutover tickets.
