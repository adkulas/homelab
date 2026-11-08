# Protected disabled DHCP staging — #107

`python3 services/network/dhcp/dhcp.py` turns the encrypted Site topology into a
Technitium scope named `home`. All allocation remains disabled. Bell continues
household DHCP. Complete the primary, secondary, backup and declared-DNS procedures
before applying this to the home Site; this command does not commission those
prerequisites or perform the supervised household handoff.

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| `read`, `plan` | Read-only, redacted changes and keyed plan ID | #107; ADR-0006 | HTTP fixture tests see no mutations or decrypted addresses/MACs/tokens |
| Scope options | Validated reservation/dynamic bands, gateway, ordered primary/secondary resolvers and `home` suffix | Encrypted Site Inventory, #98 | Independent worked topology and pinned-image readback |
| `apply --plan ID` | Exact approval for every scope/reservation change, fresh paired backup, revalidation after staging | #107; ADR-0007 | Missing/stale approval and corrupt backup rejected before mutations |
| Scope creation/update | Build in a disposable internal bridge; import only one disabled scope; no target `set` or `enable` | #99 scope-set finding | Pinned CLI import and restart rehearsal rejects direct target DHCP writes |
| Repeated apply | No duplicate/changed reservations; precise declared changes/removals; preserve unknown data | #107 | HTTP idempotency/ownership tests and real-image second plan/apply |
| Isolated clients | Reserved/dynamic leases, options and A/PTR lifecycle after explicit isolated enable | #107 | Staged client rehearsal on unpublished Docker bridge |
| Household boundary | No household broadcast path during staging or client rehearsal | #107 | Internal staging bridge; pilot identity, network and port guards; no household enable command |

## Inputs and policy

Use the same mode-0600 private connection JSON and primary HTTPS origin/CA as
[declared DNS](declared-dns.md). Use a **separate DHCP token** with Settings View,
Administration View, DHCP View, and the permissions needed for scope-only backup
and restore (Settings Modify and DHCP Modify/Delete in the pinned release).
The scope-only restore API is a privileged mutation boundary. Do not give this token,
the recovery identity, or a captured approved plan to a Git polling service.

Install Python 3, PyYAML, SOPS, age, Docker and the pinned Technitium image. The
existing [paired backup gate](backup-restore.md) checks decryption, integrity and
freshness, with a 24-hour maximum age. It does not bind the archive to the endpoint:
capture the correct Site's paired pre-change backup immediately before applying.
Backup credentials stay separate from deployment credentials.

The command consumes `site.yaml`, referenced Host and Stack Instance declarations,
`network.json`, and the encrypted Site-sensitive document. Validate the Site with
`homelab site validate --site home` first. DHCP must belong to the declared primary.
Addresses must be usable in-subnet and unique; static Hosts/gateway must be outside
the scope, and reservations require unique unicast Ethernet MACs and addresses in
the reservation band. Reservation names must be single DNS labels and avoid declared
Host/alias names and `cluster.home`.

The scope spans reservationStart through dynamicEnd. Its exclusion spans
reservationStart through the address immediately before dynamicStart, including
any gap after reservationEnd. Thus only the declared dynamic band allocates freely.
Reservations use declared names, MACs and addresses; a private journal outside Git
tracks verified ownership for removals. The journal must have one canonical path per
primary, and is locked during commands. Recover it from private state before retiring
reservations; do not delete it to bypass ownership. Unknown scopes are reported by
keyed IDs and preserved; overlapping scopes and unknown reservations block apply.
Nondefault unowned options/timers also block replacement, avoiding silent resets.

The initial scope uses one-day leases, both declared resolver addresses in order,
`home` domain/search options, DNS updates enabled in configuration, dynamic overwrite
disabled, MAC-based reservation matching and locally administered MAC support.
Allocation is disabled and all scopes must be lease-free. DNS TTL (900 seconds),
offer delay (zero) and ping policy (disabled, 1000 ms, two retries) retain the pinned
release defaults. This command refuses an enabled scope or any retained lease.
There is no enable operation. The declared-DNS collision gate remains unchanged:
lease-name protection and hostile-client testing are prerequisites for a later
supervised handoff, since DHCP updates can overwrite Git-owned DNS even when RFC
2136 updates are denied. A disabled, lease-free scope is safe to stage here.

## Operator commands

Set `SITE` to `sites/home`, `CONNECTION` to the private connection JSON, and `STATE`
to a dedicated private DHCP journal path outside Git. All errors are redacted.
`--isolated` permits loopback HTTP fixtures only and bypasses no approval gates.

```bash
python3 services/network/dhcp/dhcp.py read --site "$SITE" --connection "$CONNECTION" --state "$STATE"
python3 services/network/dhcp/dhcp.py plan --site "$SITE" --connection "$CONNECTION" --state "$STATE"
```

Review semantic changes, unknown entries and blockers. Copy the exact current ID to
`PLAN`, capture the paired backup, then apply from the operator console:

```bash
python3 services/network/dhcp/dhcp.py apply --site "$SITE" --connection "$CONNECTION" --state "$STATE" \
  --plan "$PLAN" --backup-archive "$BACKUP" --backup-identity "$IDENTITY"
python3 services/network/dhcp/dhcp.py verify --site "$SITE" --connection "$CONNECTION" --state "$STATE"
python3 services/network/dhcp/dhcp.py plan --site "$SITE" --connection "$CONNECTION" --state "$STATE"
```

Expect verified allocation `disabled` and an empty second plan. Unchanged apply
requires no backup or approval and sends no API writes. Every change requires
approval, including reservation additions. Plan IDs bind Git HEAD, source hashes,
resolved values, connection/token, implementation, ownership and live configuration.
Cluster heartbeat and synchronization timestamps are excluded; member identities,
roles and connection states remain bound to approval.
Changed input invalidates approval; re-plan after committing code or declarations.
The command re-reads everything after backup verification and disposable staging,
immediately before import. Stop independent console/API writers during apply.

The staging container is pinned, nonprivileged, attached only to a new internal
Docker bridge with no published ports, and uses an anonymous disposable volume.
Its scope-set call may enable allocation there. After disabling it, only
`scopes/home.scope` is exported, its pinned serialization disabled byte is cleared,
and a reimport/restart proves disabled state, option/reservation retention and no
leases. Container, volume, network and temporary credentials are removed before
the household target receives the archive. No zones, authentication, settings or
other scope files are imported; existing unrelated scopes remain intact.

An import/API/readback failure returns nonzero without advancing ownership. The
restore API is not a transaction: inspect private state before retrying. Use a fresh
exact plan to correct declarations or the [supervised recovery path](backup-restore.md)
for rollback. Keep DHCP disabled throughout; never restore enabled scope state to a
household-connected server. A concurrent writer can invalidate verification, so a
successful command is evidence of its readback, not a household packet capture.

## Checks and commissioning evidence

```bash
go test ./tests/acceptance -run '^TestNetworkDHCPCommands$'
HOMELAB_LIVE_DHCP=1 python3 -B -m unittest discover -s services/network/dhcp -p 'test_*.py' -k pinned -q
services/network/pilot/rehearse.sh staged_dhcp_check.py
```

The first command uses HTTP/SOPS fixtures and real age-encrypted paired archives;
it needs the same Python/PyYAML/age tools as declared DNS and no Docker daemon.
The second creates a fresh internal Docker target, runs the real operator CLI,
verifies exact options/reservations, an empty second plan and unchanged apply, and
restarts it to check disabled persistence. Its backup is a synthetic paired fixture,
not evidence of household recoverability. The last command creates fresh primary
and secondary pilot instances, establishes the test cluster, imports the staged
disabled scope, then explicitly enables allocation **only on that pilot bridge**.
It tests reserved and dynamic clients, both resolver options, home suffix, A/PTR
creation/replication/removal/expiry, and exactly one isolated offer source, then
removes the scope and disposable services. It refuses existing pilot state. Docker,
Compose, dig, OpenSSL and the pinned client/probe images are required.

[Staging evidence](../dhcp/evidence.md) records the disposable results. Household
application still requires validated private topology, commissioned primary and
secondary, fresh real paired backups, private console readback and a supervised
packet check that Bell remains the only household DHCP responder. No household
scope or network setting is changed by the repository tests.

Pinned sources: [DHCP API](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/WebServiceDhcpApi.cs),
[scope serialization](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/Dhcp/Scope.cs),
and [API contract](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/APIDOCS.md).
