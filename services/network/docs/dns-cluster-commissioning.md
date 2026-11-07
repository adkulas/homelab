# DNS Cluster commissioning — #106

The home Site DNS Cluster was joined and verified on Technitium v15.6.2. The final
operator-run outage drill completed at **2026-11-07 02:38:52 UTC** (November 6 in
Toronto); an independent read-only verification at **02:56:50 UTC** found both
members healthy. Bell DHCP and client DNS advertisement were left unchanged.

## Acceptance evidence

| Requirement | Measured evidence |
| --- | --- |
| Matching home/reverse authority and independent uncached external resolution | Both resolvers passed all 18 declared A/CNAME/PTR queries and authoritative home/reverse checks. During each peer's service stop, the survivor retained those answers and resolved a positive external A query over UDP and TCP after its own cache was flushed before each query. Replies had NOERROR and recursion available. Neither Host uses global forwarders, a proxy, Forwarder zones or DNS Apps. |
| Replicated filtering and relevant configuration; observable divergence and transfer age | Allowed/Blocked exports and the observer's documented resolver-policy fields matched. A temporary `example.com` Blocked entry synchronized, returned NXDOMAIN through both survivors during their respective outages, and was removed afterward. The observer reported no divergence. Configuration sync time, secondary refresh/expiry data and actual transfer-log timestamps were captured separately. Downloaded block lists are not configured; this exercised Technitium Blocked-zone filtering. |
| One-Host outage survival and a promotion procedure before the 72-hour margin | Each DNS service was stopped separately, its survivor verified, and the stopped service restored to healthy cluster membership before the next phase. The primary-outage snapshot yielded a conservative promotion deadline before 2026-11-10 02:38:09.550237 UTC, using the procedure below. |
| Pi DHCP disabled; no simultaneous DNS restart | Both Hosts had zero enabled scopes after the drill and no UDP 67 listener at independent verification. The recorded sequence restored and verified the Pi before stopping homelab, then restored and verified homelab. No DHCP, Host addressing or router change was part of either plan. |

The normal roles remain `homelab-dns` Primary and `raspberrypi-dns` Secondary.
Both endpoints served the reviewed CA-issued leaves covering their `.home` and
`.cluster.home` management names. The public Site CA was installed in the Pi's
system trust store for verified bootstrap; no CA root private key was transferred
to either DNS Host. Cluster peer links use the supported Technitium TLS/DANE path,
with certificate-error bypass disabled during join.

## Recorded drill sequence

| Stopped DNS Host | Survivor | Recorded stop/probe/start control window (UTC) | Result |
| --- | --- | --- | --- |
| raspberrypi | homelab | 02:37:55.167–02:38:01.291 | 18 local queries, home/reverse authority, filtering and fresh external UDP/TCP answers passed; Pi recovered to healthy membership before the next stop. |
| homelab | raspberrypi | 02:38:22.814–02:38:26.919 | The same checks passed; homelab recovered, both members became healthy and the temporary block was removed by 02:38:52.326. |

The control windows include service command and probe time; service readiness was
verified afterward. This was a short service-stop drill. A 72-hour endurance run,
physical Host power loss, role promotion and rejoin were not exercised. The
[supported promotion, fencing and rejoin procedure](dns-cluster.md#promotion-fencing-and-rejoin)
is documented for an actual primary failure.

## Sync, transfer and promotion margin

At 02:56:50 UTC the observer reported configuration sync age 181 seconds, home
serial 23 and reverse serial 19, with approximately seven days of secondary
lifetime remaining. Both query verification and repeat declared-DNS plan passed;
the latter had no changes or blockers.

At the separate log inspection at 02:58:55 UTC, the last successful **actual zone
transfers** were:

| Zone | Last successful transfer (UTC) | Transfer age at inspection |
| --- | --- | --- |
| home | 2026-11-06 02:28:08 | 88,247 seconds |
| reverse | 2026-11-07 02:38:37 | 1,218 seconds |

An unchanged SOA serial permits a successful refresh without a new transfer. That
explains why the home transfer age exceeded one day while its latest refresh was
recent and its expiry margin remained healthy. These timestamps come from the
[pinned secondary-zone success log](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/Dns/Zones/SecondaryZone.cs),
not from record modification times or expiry arithmetic.

Immediately before stopping homelab, the Pi reported these deadlines:

| Zone | Last successful refresh (UTC) | Secondary expiry (UTC) | Promote before (UTC) |
| --- | --- | --- | --- |
| home | 2026-11-07 02:38:09.550237 | 2026-11-14 02:38:09.550237 | 2026-11-10 02:38:09.550237 |
| reverse | 2026-11-07 02:38:19.886741 | 2026-11-14 02:38:19.886741 | 2026-11-10 02:38:19.886741 |

The earlier deadline governs both zones: promote before the minimum of last
refresh plus 72 hours and actual expiry minus a 24-hour recovery reserve. The
former primary must be fenced first. These are historical drill snapshots;
normal refresh resumed after recovery. Recompute both deadlines for each future
incident rather than reusing these dates.

## Ownership and defects resolved during commissioning

The observer initially looked for `configLastSynced` at the response root, following
an API example. The pinned serializer emits it inside the `Secondary`/`Self` entry
in `clusterNodes`; the code and HTTP fixture now use that shape.

The first Pi restart exposed a competing PTR writer: Technitium regenerates member
PTRs from cluster identities and address-record TTLs. The operator approved
[ADR-0010](../../../docs/adr/0010-let-technitium-own-dns-cluster-member-ptrs.md).
Both DNS member PTRs are now Technitium-owned and verified against validated
cluster A records; Git retains `.home` A records, aliases and other Hosts' PTRs.
A supported same-address metadata refresh migrated homelab's remaining PTR and an
empty DNS apply recorded ownership in the private journal. Both subsequent service
restarts passed health verification without Git PTR repair.

## Private evidence and recovery

Full receipts and logs remain outside Git in the administrator's private
`$HOME/.local/state/homelab-dns/cluster-106/recovery/` packet. The completed run is
`completed-20261107T025855Z`; it includes the exact migration and runtime outage
plans, survivor probe receipts, TLS installation receipt, final verification,
actual transfer logs and checksummed index. Operational addresses, MAC addresses,
tokens, passwords and decrypted DNS snapshots are excluded from this record.

The final post-drill paired archive is `dns-cluster-20261107T025855Z.age`, SHA-256
`e4b7a69d4a3d223661af87562240010426b1e2e3366257935e27b272142fe858`.
Capture, decryption and integrity verification passed on the administrator PC.
The CA root archive remains separate. External-media replication and a full paired
Restore Drill are outside this commissioning record; use the
[backup and recovery procedure](backup-restore.md) for those operations.
