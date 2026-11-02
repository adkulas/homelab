# Git-owned local DNS — #105

`python3 services/network/dns/dns.py` provides redacted `read`, `plan`, `apply`,
and `verify` commands for the primary DNS Stack Instance. This is a supervised
DNS-only operation: Bell retains DHCP and advertised client DNS settings. It does
not commission either Host, establish the DNS Cluster, or install a polling timer.

## Behavior and evidence

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| `read`, `plan` | No DNS mutation; precise changes identified by semantic record IDs, action, TTL and whether values changed; no decrypted addresses | #105; ADR-0006 | CLI tests check no API writes and redact IPs, reverse names and tokens |
| `apply` | Routine service alias additions/updates apply once; repeat apply is empty | #105; ADR-0007 | CLI alias lifecycle and idempotency tests |
| `apply --plan ID` | Entire mixed plan stops for protected Host, NS/SOA, reverse-zone or deletion changes; claiming an existing manual record is protected | #105; ADR-0007 | Exact approval, protected drift, fixed-record change/removal and reverse migration tests |
| `apply --plan ID` | Reject changed commit, declarations, decrypted inputs, ownership, endpoint or live configuration; reject an incompatible release or secondary endpoint | #105; pinned API | Stale approval, endpoint binding and version/role tests |
| Protected `apply` | Invoke the existing decrypt/integrity/freshness backup gate immediately before mutation, then re-read declarations, credentials, ownership and live state | Backup runbook; ADR-0007 | Real age-encrypted paired archive tests reject missing, corrupt and stale backups |
| `apply`, `verify` | Check API state and exact A, CNAME and PTR answers through the declared primary resolver; publish ownership only after success | #105 | Query mismatch and API failure tests return nonzero without publishing successful ownership |
| `plan`, `apply` | Report unknown records, preserve them; block retirement of a zone containing unknown records | #105 | Unknown alias collision, preservation and retired-zone tests |
| `plan`, `apply`, `verify` | Deny external dynamic updates; reject enabled DHCP DNS publishers under every suffix, including PTR overwrite paths | #105; pinned DHCP implementation | Zone-security and DHCP collision tests |

Run command tests with `go test ./tests/acceptance -run '^TestNetworkDNSCommands$'`.
They use a disposable HTTP API, fixture SOPS/dig processes, and real age encryption.
They do not contact the household DNS service. Live DNS and hostile-client probes
below remain required commissioning evidence.

## Inputs and ownership

Install Python 3, PyYAML (the reproducible Python dependency is in
`services/network/dns/requirements.txt`), SOPS, age and `dig`. Run commands from the
repository checkout. The repository-wide Site validation/planning interface stays
read-only; this Stack command is the mutation boundary.

The Site Inventory supplies the Hosts, primary/secondary DNS Stack Instances,
network subnet reference and SOPS document reference. The command decrypts that
single Site-sensitive document to memory. Every addressed Host gets one A and PTR
record. The reverse zone derives from the subnet; this initial implementation
supports IPv4 octet-aligned /8, /16 and /24 zones and rejects other prefixes.

[The home DNS policy](../../../sites/home/network.json) declares aliases by Host
identity, a shared TTL and SOA refresh/retry/expiry/minimum. It contains no addresses.
All policy fields are required; unsupported/duplicate fields and invalid alias
references fail closed. The aliases match the existing home Traefik routes. Both
zones get an apex NS pointing to the primary Host and an SOA with a seven-day
expiry, above the 72-hour service margin. SOA serials and serial date schemes
remain runtime state, rather than Git-owned counters.

`cluster.home` and its descendants are reserved for Technitium. Alias declarations
cannot claim them. Existing cluster records/configuration are observed and included
in the approval fingerprint; the command never writes them. Cluster initialization,
catalog membership and Technitium-owned clustered NS/SOA integration belong to #106.
Do not join a catalog and then force this pre-cluster policy over its generated NS/SOA.

An ownership journal outside Git records the last successfully verified zones and
record sets. It is mode 0600, bound to the Site path, primary instance and API origin,
and protected by a per-journal process lock. Keep one canonical journal path per
primary; concurrent invocations must use it. It contains sensitive values and must
remain private. Keep it alongside private runtime state; do not commit or share it.
Losing it loses the history needed to distinguish removals from unknown records;
recover the last verified journal before planning deletions. A first apply can adopt
declared live record sets; changing an existing manual record requires exact approval.
Unrelated live records are never adopted or pruned. Unknown entries use stable keyed
IDs; inspect their details privately in the Technitium console.

The plan ID is a keyed digest of the Git HEAD, input file contents, resolved values,
implementation, private ownership and live DNS/DHCP/cluster configuration. Rotating
the API token invalidates plans. Query timestamps and display strings are excluded;
ACL order is preserved. The plan JSON contains neither raw values nor credentials.
No plan file containing decrypted topology is written.

## Prepare — operator console

Use the already commissioned primary's trusted management HTTPS origin and CA.
Create a dedicated API token with Settings View, Administration View, DHCP View,
Zones View/Modify, and View/Modify/Delete on the owned zones (including permission
to create/delete them). Keep backup credentials separate. Existing zone permissions
must allow this token to read every scoped zone, including observed cluster zones.

In a mode-0700 directory outside Git, create a mode-0600 token file and a mode-0600
connection JSON using a private editor. The connection has this shape (substitute
private file paths and the commissioned origin locally):

```json
{
  "instance": "homelab-dns",
  "url": "https://homelab.home:53443",
  "tokenFile": "/private/network/dns.token",
  "caFile": "/private/network/home-ca.crt"
}
```

The origin must already resolve to the verified primary address independently of
this change, for example via the commissioned recovery client's private hosts entry.
TLS verification is mandatory. Tokens use bearer headers; redirects and environment
HTTP proxies are disabled. `--isolated` permits HTTP only to a loopback origin for
disposable fixtures, and does not bypass approval, backup or verification gates.

Set `SITE=sites/home`, `CONNECTION` to the private connection file and `STATE` to
a fresh private journal path. Confirm the primary-only prerequisites and recovery
route in [primary commissioning](primary-commissioning.md). Validate the encrypted
Site topology using the [Site Inventory procedure](../../../docs/site-inventory.md)
before changing records. No Host address or DHCP setting is applied by this command.

## Plan and apply — operator console

```bash
python3 services/network/dns/dns.py read \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE"
python3 services/network/dns/dns.py plan \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE"
```

Review all `changes`, `unknown` entries and `blockers`. An empty plan is expected
on an unchanged verified declaration. Unknown entries alone do not block routine
changes, but an alias/CNAME collision blocks the entire apply. A removed owned
record is an explicit protected deletion. When the subnet changes, the command
creates the new reverse zone and records, then retires the old owned zone; unknown
records in the former zone block the whole operation until relocated by the operator.

For a plan containing only routine alias additions/updates:

```bash
python3 services/network/dns/dns.py apply \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE"
```

For any protected change, copy the current `plan` ID into `PLAN`, create a fresh
[paired backup](backup-restore.md), and supply the encrypted archive and recovery
identity paths. The command invokes `backup.py gate --max-age-hours 24` itself:

```bash
python3 services/network/dns/dns.py apply \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE" \
  --plan "$PLAN" --backup-archive "$BACKUP" --backup-identity "$IDENTITY"
```

The initial apply is protected. The backup gate currently requires **both** DNS
Stack Instances. The historical primary-only commissioning archive is not a paired
backup and cannot satisfy it: obtain a fresh paired backup after the secondary is
available before applying this command to the commissioned Site. No primary-only
backup bypass is provided. The inherited backup gate verifies decryption, integrity
and age; its manifest does not bind an archive to this endpoint or exact live state.
The operator must capture the correct Site's fresh paired backup immediately before
the protected change. The 24-hour ceiling is not permission to substitute an unrelated
or earlier pre-change archive. Routine polling must not receive the recovery identity.

Any blocker, stale approval, failed backup or API/DNS failure returns nonzero with a
redacted error. Re-plan and review; do not retry an obsolete approval. API mutations
are not a transaction: an API failure can leave partial DNS changes. The command
stops, never reports success or advances the ownership journal on failure, and does
not automatically restore the entire server. Keep the exact plan and private
pre-change journal/backup available for supervised recovery. The local lock prevents
concurrent runs through this journal; stop unrelated console/API writers during apply.

## Verify, collision checks and rollback

```bash
python3 services/network/dns/dns.py verify \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE"
python3 services/network/dns/dns.py plan \
  --site "$SITE" --connection "$CONNECTION" --state "$STATE"
```

Expect `status: verified`, the declared A/CNAME/PTR query count and then no changes.
Unknown runtime records may still be reported. From a wired test client query the
primary explicitly for each declared Host, an alias and a PTR; inspect exact values
privately. Attempt an RFC 2136 update against a declared name from an ordinary client:
it must be refused and all declared answers must remain unchanged. Verify normal
uncached recursion and that Bell remains the only DHCP responder.

The command sets zone `update=Deny`. Technitium's internal DHCP updates bypass that
RFC 2136 policy: the pinned source skips its existing-A check for reserved leases.
The DHCP INFORM path can also overwrite a declared PTR from a scope with a different
forward suffix. Accordingly, this DNS-only stage rejects every enabled scope with
DNS updates, regardless of suffix or dynamic-overwrite policy. A disabled scope
staged to publish under `home` is also rejected; an unrelated disabled factory
sample scope can remain. #107 must establish and
prove safe lease-name ownership before allowing those updates; this command never
creates, enables or edits a DHCP scope. The fixture test proves the fail-closed gate,
not a live hostile-client packet exchange.

For a routine rollback, revert the relevant policy change, generate a fresh plan
and apply it under the same rules (an alias removal is protected). For a failed
protected operation, stop further applies, inspect the exact affected records in
the private console and use the [backup recovery procedure](backup-restore.md) if
needed. Keep DHCP disabled during DNS recovery. Do not delete the journal to force
an empty history, prune unknown records, or restart both DNS Hosts together.

Pinned primary sources: [API contract](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/APIDOCS.md),
[record serialization/writes](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/WebServiceZonesApi.cs),
and [DHCP collision handling](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DnsServerCore/Dhcp/DhcpServer.cs).
