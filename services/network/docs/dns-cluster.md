# Site DNS Cluster — #106

This is the supervised join, observation and recovery procedure for the
`homelab-dns` and `raspberrypi-dns` Stack Instances. Bell retains DHCP and client
DNS advertisement. This change does not enable DHCP, change Host addresses or
install an automatic cluster mutation command.

## Behavior matrix

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| Technitium console join | Two Host-assigned members, local and reverse zones in the cluster catalog, trusted direct HTTPS | #106; Site Inventory; pinned cluster API | Private before/after snapshot and live commissioning checks below |
| Declared-DNS CLI | Preserve generated two-member NS and clustered SOA MNAME; retain Git-owned SOA timers, Host A, other Host PTRs and aliases; verify cluster-owned member PTRs; fail closed on wrong membership or catalog | #105/#106; ADR-0007; pinned cluster manager | Catalog alias lifecycle, member PTR ownership, address-source and retirement regression tests |
| `cluster.py` | Read both Hosts directly; compare declared records, full local record sets/serials, filtering and relevant resolver policy; expose sync age and remaining secondary lifetime | #106; pinned API | Isolated CLI acceptance checks for healthy and symmetric divergent states |
| Operator outage drill | Either survivor answers local/PTR, filtered and fresh external queries; fence former primary before promotion before the 72-hour margin | #106; supported promotion API | [Measured one-Host drill and documented promotion deadline](dns-cluster-commissioning.md) |
| Every operation | Pi DHCP disabled; no simultaneous service restart | #106; ADR-0007 | Read-only observer rejects enabled scopes; measured sequential recovery and absent UDP 67 listeners |

The repository tests run through `go test ./tests/acceptance -run
'^TestNetworkDNSCommands$'`. They use isolated HTTP/process fixtures, not household
DNS. They establish command behavior, not live join, TLS, filtering, outage survival
or promotion success. The [commissioning record](dns-cluster-commissioning.md)
separately records the completed live join, filtering and one-Host outage checks.

## Ownership and pinned behavior

Both instances must run pinned Technitium v15.6.2. Use
[the pinned API reference](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/APIDOCS.md#administration-api-calls)
and [cluster manager](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/Cluster/ClusterManager.cs),
particularly `UpdateClusterRecordsFor`, `AddClusterPrimaryZoneRecordsFor` and
`PromoteToPrimaryNodeAsync`. The console is the supported mutation interface here.

The Site Inventory owns placement and normal primary/secondary roles. Technitium
owns `cluster.home`, `cluster-catalog.cluster.home`, node identity, certificates,
catalog membership, generated NS, SOA MNAME and **both DNS members' PTR records**.
Git owns the `.home` Host A records, service aliases, other Hosts' PTRs, SOA timers
and contact, ordinary record TTL and the no-client-update policy. This ownership
boundary is recorded in [ADR-0010](../../../docs/adr/0010-let-technitium-own-dns-cluster-member-ptrs.md).

A member PTR targets its `.cluster.home` identity and uses the TTL of its cluster A
record. Before trusting that source, both tools require one enabled, non-expiring A
record whose address matches the member's Site Inventory address and whose TTL is
valid. They reject missing, disabled, foreign-address and malformed sources. The
observer checks each Host's own source independently. `.home` A answers and aliases
remain unchanged, and nonmember PTRs keep the Git-declared target and TTL.

Joining, restarting or updating a member can regenerate its PTR. The DNS CLI verifies
these records and blocks on member PTR drift; it never adds, overwrites, re-enables
or deletes them. Do not restore `.home` member PTRs from Git. After a fresh paired
backup and exact change review, use Technitium's supported **Update Node IP Addresses**
action with the same verified declared address to refresh that member's metadata;
this updates cluster DNS metadata, not Host networking. Verify both resolvers and
repeat the plan. Investigate an invalid cluster address source before any refresh.

The private journal marks observed member PTRs with `owner: technitium-cluster`.
An empty apply can migrate an older journal after live verification without a DNS
write. Historical cluster-owned records that are no longer declared are reported as
unknown rather than deleted by Git; they can block retired-zone deletion until the
operator resolves their ownership. Leaving the Cluster and returning member PTRs
to Git produces protected changes requiring a fresh exact plan. Pause unrelated
console/API writers throughout every change window.

Cluster synchronization overwrites the Pi's Allowed, Blocked, Apps, shared Settings
and Administration configuration, including users, permissions and API tokens.
The Pi's prior credentials may stop working. Cache, logs, sessions, node-specific
listener/TLS settings and DHCP scopes are independent. Keep DHCP disabled on both
Hosts throughout this DNS-only stage. Local recursion must not forward to the peer;
this procedure requires no global forwarders or proxy on either member. Confirm
there are also no catch-all Forwarder zones or DNS Apps redirecting external queries.

## Prepare an exact supervised change

1. Validate the encrypted Site Inventory and both commissioned static addresses.
   Use the recovery client's private hosts entries and the trusted direct management
   HTTPS origins from [management HTTPS](management-https.md). Check the presented
   certificates and SANs for the future `homelab.cluster.home` and
   `raspberrypi.cluster.home` identities as well as the commissioned names. Reissue
   leaves through the offline CA procedure if necessary, secondary first. Never
   bypass certificate errors or put a reverse proxy in the peer connection.
2. Confirm private primary and Pi console access, API token recovery, CA trust,
   restricted LAN/VPN access, DNS UDP/TCP 53 between members, and direct HTTPS TCP
   53443 between members. The CA root key stays offline. Keep a recovery client
   independent of the names being changed.
3. Pause reconciliation and unrelated console writers. Save private snapshots of
   both instances' version, settings, cluster state, zones/options/records, Allowed,
   Blocked, Apps, Administration and DHCP scopes. Keep tokens, addresses and full
   snapshots outside Git in mode-0700 storage, files mode 0600. Capture a fresh
   paired encrypted backup and pass `backup.py gate --max-age-hours 24` as described
   in [backup and recovery](backup-restore.md). Check recovery identity availability.
4. Write a private exact change sheet: current commit and snapshot digests, the two
   instance/Host identities and verified addresses, direct HTTPS peer origin,
   `cluster.home`, catalog name, the exact home/reverse zones, filtering policy
   being copied, SOA refresh/retry/expiry, and the member PTR ownership handoff. Include Pi
   overwrite effects, console credential recovery and rollback steps. The operator
   approves this sheet before mutation, per ADR-0007. Immediately re-read the
   snapshots before each phase; any changed input or state invalidates that phase's
   approval. Review a new sheet rather than proceeding with an obsolete one.

There is no automated cluster apply or stale-plan bypass. The console operator
performs the before/after comparison. The declared-DNS mutation tool separately
rejects stale plan IDs and gates protected record changes with the paired backup.

## Join, catalog and filtering — console operator

1. Verify the declared-DNS plan is empty on the standalone primary. Initialize its
   cluster with domain `cluster.home` and its verified static address. Confirm its
   identity becomes `homelab.cluster.home` and the cluster catalog exists. Verify
   DNS and trusted HTTPS on this Host before proceeding; no restart of the Pi.
2. At the Pi, initialize/join using its verified static address, the primary's
   trusted direct cluster HTTPS origin and verified primary address. Supply primary
   administrator credentials privately. Leave **Ignore Certificate Errors off**.
   The API equivalent is `admin/cluster/initJoin` with
   `ignoreCertificateErrors=false`; never use the pilot's insecure bootstrap flag
   on the Site. Stop on any TLS or identity failure.
3. Reauthenticate the Pi using synchronized credentials. Verify both views show
   exactly the declared primary and secondary, with Self/Connected states. Confirm
   both TLS listeners and Pi-local restrictions survive; no enabled DHCP scope is
   allowed on the Pi. Inspect scope lists on both Hosts and capture DHCP traffic
   from the test client: Bell remains the only offer source.
4. At the primary, add the **existing** `home` and derived reverse zone to
   `cluster-catalog.cluster.home` using zone options. Do not recreate or delete the
   zones. Retain `update=Deny`; use the cluster catalog's generated transfer/notify
   policy, without opening transfers globally. Verify both become Secondary zones
   on the Pi and their NS sets include exactly both cluster names. Confirm SOA
   expiry is the declared 604800 seconds (seven days), refresh 900 and retry 300.
5. Run the declared-DNS `plan`. Generated clustered NS, SOA MNAME and member PTRs
   must not be proposed for Git replacement. On a member PTR blocker, review and
   apply the supported metadata refresh described above under its own exact plan
   and fresh paired backup. Then verify, repeat plan and expect no changes. An
   empty apply records the observed PTR ownership in the canonical journal without
   changing DNS. Keep that journal; do not discard it to hide drift.

6. In the primary console review the exact intended filtering policy (Allowed,
   Blocked, block-list URLs, blocking response and bypass lists) and relevant shared
   resolver settings. Use the supported secondary Resync Cluster action if needed;
   never copy DHCP scopes or replace node-specific TLS/listener settings. Wait for
   synchronization, then run the observer and the DNS probes below. Inspect Apps
   and Administration separately: the observer compares the named policy fields,
   not every cluster-owned application or user permission.

## Observe without mutation

Use private connection JSONs with the same `instance`, `url`, `tokenFile`, `caFile`
shape as [declared DNS](declared-dns.md). Use distinct direct trusted HTTPS origins
and tokens with Administration, Settings, Allowed, Blocked, DHCP and Zones View,
plus zone-specific access needed for records/options. The pinned options read API
also requires Zones Modify permission; the observer makes no mutation calls.

```bash
python3 services/network/dns/cluster.py --site sites/home \
  --primary-connection "$PRIMARY_CONNECTION" \
  --secondary-connection "$SECONDARY_CONNECTION"
```

Exit 0 means the observed state is healthy; exit 1 and JSON `degraded` reports
semantic issue IDs, and `unavailable` means an observation could not be completed.
No raw names, addresses, filtering entries, secrets or upstream API errors are
printed. An unreachable member cannot be reported healthy. There is no peer proxy,
cache flush, service restart or write. `--isolated` permits loopback HTTP fixtures
only and is unsuitable for the Site.

The observer compares Allowed/Blocked exports, the blocking settings, recursion
policy/ACL, DNSSEC validation, minimum SOA timers, forwarders and proxy, plus the
complete local record sets and serials. It checks declared A/CNAME and nonmember
PTRs against Git, and member PTRs against validated cluster address records,
member identity/roles, catalog membership and cluster-zone availability. It rejects
configuration sync age above 1800 seconds and remaining local-zone lifetime at or
below 259200 seconds. ACL order remains significant. Equal empty filtering lists
are permitted, but blocking must be enabled; prove actual filtering below.

`configAgeSeconds` measures the Pi's last complete configuration sync. Its timestamp
is read from the `Secondary`/`Self` entry in `clusterNodes`, as emitted by the
[pinned state serializer](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/WebServiceClusterApi.cs).
`remainingSeconds` is derived from the secondary zone's reported expiry deadline;
`refreshAgeSeconds` is SOA expiry minus remaining lifetime. A successful refresh
without a changed serial resets this deadline without transferring new records.
Consequently **refresh age is not transfer age**. Record the last successful zone
transfer timestamp from each zone's Technitium transfer logs, its current serial,
and elapsed age in the private commissioning record. If logs are missing, record
transfer age as unknown and repeat a supervised harmless alias change to observe
an actual transfer. Do not infer it from last-modified record timestamps.

## Live probes and one-Host outage drill

Record times in UTC and keep sensitive dig output privately. From a wired recovery
client query each resolver explicitly over UDP and TCP: both Host A records, at
least one service alias, both PTRs, `home` and reverse SOA/NS. Require matching exact
answers, authoritative flags for local authority and equal SOA serials. Compare the
observer output and transfer logs after a supervised routine alias addition, then
remove that alias with a protected exact plan and verify replication again.

Use a privately reviewed, normally resolving test domain as a temporary Blocked
entry on the primary. Require the expected blocked response on both Hosts, observe
its removal synchronizing, and require positive resolution again. Check a domain
from the configured downloaded lists separately if those lists are used: equal URL
configuration alone does not prove equal downloaded content or working filtering.

For fresh external resolution choose separate never-queried names in an
operator-controlled positive wildcard zone. Record their successful A answers,
NOERROR and recursion-available flags from each resolver. If this is unavailable,
clear only the survivor's cache through the console before querying a known positive
external name. Record the cache clear and fresh answer; a cached answer or mere
NXDOMAIN from a random public name does not establish this criterion.

1. With both members healthy, stop **only the Pi DNS service**, using its installed
   systemd unit. Keep homelab running. Repeat local/PTR, filtering and fresh external
   probes through homelab. Start the Pi, wait for full catalog/configuration catch-up,
   and require healthy observation and equal serials before touching homelab.
2. Record each Pi local zone's last successful refresh and actual expiry deadline.
   Set a supervised promotion deadline **before last refresh + 72 hours**, and also
   before either measured zone expiry minus a 24-hour recovery reserve, whichever
   is earlier. Start the drill with more than 72 hours remaining on both zones;
   seven-day SOA expiry supplies margin, not proof of a 72-hour soak.
3. Stop **only primary DNS** using its deployed Compose service. Verify the Pi still
   answers authoritative local/PTR, filtered and fresh external queries. Record
   survivor results and remaining lifetime. The read-only observer will be
   unavailable while primary is stopped; this is expected, not a passing health
   report. Do not wait until expiry to begin recovery.
4. Choose ordinary restart recovery or the separately approved promotion below.
   For ordinary recovery restart only homelab, wait for synchronization and repeat
   all checks. For a full 72-hour endurance claim actually collect the complete
   interval's probes and resource samples; a short outage proves only that interval.

## Promotion, fencing and rejoin

Before promotion, fence the former primary so it cannot return to LAN DNS service
or mutate cluster state. Disable its service auto-start and isolate its DNS and
peer-management connectivity while retaining a private recovery route. Stopping a
container alone is insufficient fencing if its restart policy can start it again.
Record the fence and its verification in the approved recovery change sheet.

At the surviving Pi console use **Promote to Primary**. If the former primary is
reachable, the normal supported operation first resyncs complete configuration and
asks it to leave. During a fenced/unreachable-primary recovery, approve the explicit
**Force Delete Current Primary Node** option: API
`admin/cluster/secondary/promote` with `forceDeletePrimary=true`. This skips the
final sync and may lose changes since the last successful transfer/config sync;
record those ages and accept that loss explicitly. Do not use Leave Cluster as a
substitute for promotion. Complete before the deadline above.

Verify Pi-local `home` and reverse zones become Primary, local/PTR and filtering
answers remain correct, fresh external resolution works, DHCP is still disabled,
and the Pi is the only primary. Promotion removes the old member; the normal
two-member observer and declared-DNS reconciler intentionally fail closed during
this recovery topology. Keep automatic reconciliation paused. Do not point the
normal primary connection/journal at the Pi to bypass Site ownership.

Keep the old Host fenced while inspecting its private backup and stale state.
Prepare a new exact rejoin plan: clear its obsolete cluster configuration through
the supported primary Delete Cluster action (force only for the isolated stale
cluster), preserve/recover credentials and private data, and resolve conflicting
standalone copies of catalog member zones while isolated. Back up before any
reviewed deletion. Verify public Site CA trust from the joining service runtime, including inside a
container, before joining that Host to the Pi as a secondary through trusted HTTPS.
Administrator browser trust alone does not establish runtime trust; container
recreation requires that trust to be checked again. Verify catalog synchronization and disable DHCP before exposing LAN DNS.
Do not restore an old cluster backup into an active unfenced peer.

A permanent role reversal requires a separate reviewed Site Inventory change and
new correctly bound ownership journal migration. For a drill that restores normal
roles, use a planned graceful promotion of the caught-up homelab secondary, then
rejoin the Pi as secondary; keep only one active primary throughout. Verify generated
member PTRs against cluster identities and address-record TTLs, refresh metadata
under a new approved plan if needed, and verify declared DNS and the two-member observer,
and resume reconciliation only when the normal Site roles and exact plan agree.
Never restart both Hosts together. If promotion or rejoin fails, keep the stale
Host fenced and use the one surviving resolver plus the recovery packet; do not
attempt blind symmetric restores.

## Commissioning evidence

The [redacted commissioning record](dns-cluster-commissioning.md) records the live
join, matching answers, filtering add/remove checks, actual transfer timestamps,
sequential service-stop drills, promotion deadlines and final healthy state. It
also distinguishes the short outage checks from unexercised 72-hour endurance,
physical Host power loss and role promotion/rejoin. Keep full sensitive output and
future exact plans in private recovery storage.
