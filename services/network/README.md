# Home Site network

Issue #97 adds a read-only preflight for the existing home Site. Run it on each available Host before changing Host addressing, DNS, or DHCP:

```bash
python3 services/network/preflight.py --role homelab
python3 services/network/preflight.py --role raspberrypi
```

Run the matching command **on that machine** from a checkout of this repository. A laptop can use `--role recovery-client` to check its local route and help prove wired recovery access. Exit code `2` means unresolved cutover blockers; `0` means all automated checks and operator confirmations passed; `64` means invalid input. The JSON report never prints an observed IP address, MAC address, DNS server address, or network profile contents.

The check reads routes, interface address metadata, relevant listeners, resolver metadata, profile counts, and tracked configuration candidates. It does not configure interfaces, query the Bell admin API, send DHCP broadcasts, restart services, or install anything. The report shows route interfaces and metrics, and listener binding types, without addresses. A local listener is evidence to review, not proof of a port conflict. An observed IPv6 resolver is not proof of how Bell advertises it. The manual checks remain blockers until separately verified.

Start with the [ordered setup and recovery walkthrough](docs/setup.md) for inventory, encrypted inputs, Host preparation, DNS, DNS Cluster, DHCP handoff, polling and outage recovery. It identifies the deployment gates still owned by later tickets. The [preflight and recovery packet guide](docs/preflight.md) contains the detailed private evidence checklist and fallback sequence.

## Management certificates

The [offline CA and management HTTPS guide](docs/management-https.md) covers the #100
certificate commands, separate encrypted root backup, administrator trust, supervised
renewal, and recovery. Prepare certificates before installing the DNS Stack Instances;
actual Host deployment and access restrictions remain deployment gates.

Run certificate acceptance checks with `go test ./tests/acceptance -run '^TestNetworkManagementCertificateCommands$'`
or `python3 -B -m unittest discover -s services/network/ca -p 'test_*.py'`.
These require Python 3, OpenSSL 3, age and age-keygen. Run
`services/network/pilot/https_check.sh` for a disposable rehearsal against both pinned
Technitium containers, including secondary leaf replacement. It requires Docker Compose,
publishes no Host ports, and shares the pilot lock with the #99 runner.

## Paired DNS backup and recovery

The [backup and recovery guide](docs/backup-restore.md) covers #101's paired encrypted
capture, PC/Passport copy verification, protected-change gate, isolated Restore Drill,
and supervised stopped configuration export for either DNS Host. Run
`python3 services/network/backup/backup.py --help` for the operator interface.
No command starts a production service or enables household DHCP.

## Primary DNS deployment

Use the [homelab primary DNS procedure](docs/primary-dns.md) for #103's production
Compose service, strict first-start preflight, private state, access restrictions,
trusted HTTPS and timed local rollback. Bell continues DHCP throughout. The production
Compose file is separate from the disposable pilot; do not import pilot configuration.
The primary was commissioned and reboot-verified; see the [redacted commissioning
record](docs/primary-commissioning.md) for results, approved access/storage choices and
private recovery locations. Bell retains DHCP and client DNS settings. Run command/artifact checks with
`go test ./tests/acceptance -run '^TestNetworkPrimaryDeployment$'` (Python 3 and Docker
Compose required; no Docker daemon or production service is used by these tests).

## Native Pi DNS deployment

Use the [raspberrypi native DNS procedure](docs/secondary-dns.md) for #104's pinned
ARM64 bundle, stopped systemd installation, private fresh state, persistent access rules,
static-address approval/rollback, trusted HTTPS and service removal. Bell retains DHCP.
SSH preparation uses the existing `adam@rasp-pi` alias; privileged actions require the
operator's local sudo session. Reboot, removal/reinstall recovery and the full 24-hour
soak passed; see the [Pi commissioning record](docs/secondary-commissioning.md) for
redacted evidence and operator-approved exceptions. Run staging command checks with
`go test ./tests/acceptance -run '^TestNetworkSecondaryDeployment$'`.

## Git-owned local DNS

Use the [declared DNS procedure](docs/declared-dns.md) for #105's redacted
read/plan/apply/verify command. The Site's `network.json` refers to Hosts while SOPS
supplies addresses. Alias additions/updates may apply routinely; protected changes
require the exact current plan and a verified fresh paired backup. Unknown live
records are reported and preserved. This DNS-only stage keeps Bell DHCP and
rejects unsafe Technitium lease-name updates.

Use the [Site DNS Cluster procedure](docs/dns-cluster.md) for supervised join,
filtering synchronization, read-only health checks and one-Host outage/promotion
drills. The [DNS Cluster commissioning record](docs/dns-cluster-commissioning.md)
contains the completed live join, filtering and sequential outage evidence, plus
the measured promotion deadlines and remaining drill limits.

The [protected DHCP staging procedure](docs/declared-dhcp.md) provides redacted
read/plan/apply/verify commands for the encrypted Site topology. Every scope or
reservation change requires an exact plan and fresh paired backup. Scope creation
runs in an internal disposable bridge; only a disabled scope is imported to the
primary. Bell continues household DHCP until the separately supervised handoff.
