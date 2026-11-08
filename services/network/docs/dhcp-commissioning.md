# Disabled DHCP commissioning — #107

The home Site's protected DHCP scope was staged on the commissioned homelab primary
on 2026-11-08 UTC. The operator completed the supervised wizard; its final receipt
at 02:18:32 UTC reported `ready-for-closure-review`. Allocation remains disabled on
both DNS Stack Instances. Bell remains the household DHCP owner; enabling Technitium
and changing Bell belong to the later supervised handoff.

## Acceptance evidence

| Required behavior | Observed result |
| --- | --- |
| Validated Site topology | Site validation passed. Private API comparison confirmed exact scope bounds, excluded reservation band, gateway, ordered primary/secondary resolvers, home domain/search suffix and MAC reservations. No operational addresses or MACs are recorded here. |
| Protected dry run and exact apply | The reviewed plan contained one `scope:home` create and no blockers. The operator approved its exact ID. Scope creation ran inside a disposable internal bridge; only its disabled scope was imported to the primary. |
| Recoverable pre-change backup | Separate backup credentials worked against both DNS nodes. A fresh paired encrypted backup was captured and verified for decryption, integrity and freshness before apply. |
| Idempotency | API verification passed, the second plan contained no changes or blockers, and unchanged second apply passed. The private ownership journal records the verified reservations. |
| Allocation remains disabled | Post-apply readback found the primary scope disabled and lease-free. The Pi had no enabled scopes and no leases. The SSH checks found no primary UDP 67 listener. |
| Isolated client behavior | The earlier [staged pilot](../dhcp/evidence.md) proved reserved/dynamic options and A/PTR creation, replication, removal and natural expiry. It used disposable bridge addresses, without household broadcasts. |
| Household offer sources | On the laptop's verified physical Ethernet interface, an Nmap DHCP DISCOVER diagnostic observed one offer response and one server identifier, matching the validated Bell gateway. No Technitium offer was observed. The diagnostic did not request a lease or change client addressing. |
| Protected automation boundary | Scope and reservation changes still require exact-plan approval and the fresh paired-backup gate. DHCP operator credentials, backup credentials and the recovery identity are separate from routine Git polling. |

The first supervised plan exposed a fingerprint bug: a cluster member's `lastSeen`
heartbeat timestamp changed the approval ID even though configuration was unchanged.
Commit `9d4d94a` applies the existing configuration normalizer to DHCP plan inputs.
The operator restarted the wizard and approved a fresh plan after the fix. A CLI
regression test proves timestamp changes preserve approval while a member role
change still invalidates it and blocks apply; the live plan stayed stable over a
36-second heartbeat observation. This was the commit used for the completed apply.

## Private recovery packet

The laptop retains the canonical DHCP journal, mode-0600 operator and paired-backup
connections/tokens, encrypted account recovery, raw diagnostic and the final receipt
under `$HOME/.local/state/homelab-dns/dhcp-107/`. The completed run directory is
`run-20261108T013807Z-xYXETr`; the household diagnostic is `household-offers.txt`,
with a redacted check receipt in `household-offer-check.json`.

The verified pre-change paired archive is
`pre-dhcp-107-20261108T014136Z-xYXETr.age`, SHA-256
`50e1d4f1ac075af5dbc9dc8c16e969781651cde483acfcd1bd9c1cb60b61a48e`.
Preserve the recovery identity independently of the DNS Hosts. Creating a local
verified archive does not establish an external-media copy or a new Restore Drill;
those follow the [paired backup procedure](backup-restore.md).

Backup users and permissions were authored on the DNS Cluster primary and verified
at their intended DNS endpoints after synchronization. Scope-only restore and backup
export require Technitium's broad Settings Delete permission; the API cannot limit
it to DHCP import or backup-only access. These are supervised operator credentials.
Existing users' and groups' permissions were preserved. No account password, token,
private key or decrypted topology belongs in the public evidence.

For later drift checks use [the declared DHCP procedure](declared-dhcp.md). Keep the
same canonical journal and generate a fresh plan from the current checkout. Continue
using Bell DHCP until the supervised handoff; lease-name collision protection and
hostile-client checks remain prerequisites before allowing household DHCP DNS updates.
