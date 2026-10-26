# Disposable Technitium pilot (#99)

This Compose project runs two pinned v15.6.2 DNS instances on a private Docker bridge. It publishes no Host ports and cannot send DHCP broadcasts to the household LAN. It is a disposable rehearsal; the production Host Compose belongs to #103. The native Pi command below uses a separate outbound-only network namespace.

The image is pinned to the official multi-platform manifest digest. v15.6.2 supersedes the plan's v15.6.1 candidate because its [release notes](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/CHANGELOG.md) fix cluster configuration sync and a DNS UDP resource exhaustion issue. Recheck the digest and native release assets before installation.

## Docker pilot on homelab

Run from the repository root:

```bash
services/network/pilot/run.sh
```

`run.sh` holds a lock so two pilots cannot share the same project. It creates a temporary private password file, runs the checks in order, and removes containers, volumes, and the password after successful teardown. A teardown failure exits with an error and a cleanup command while retaining the private credential for recovery. `check.sh` tests external DNS answers with each peer stopped in turn. `cluster_check.py` joins the two disposable instances, verifies the HTTPS and DNS TCP listeners, catalog zones, filtering policy sync, and a shortened secondary expiry. It temporarily sets the minimum SOA refresh/retry to 10 seconds and the test zone expiry to 60 seconds. Production defaults are different: v15.6.2 has a 300 second minimum refresh/retry, so a short SOA expiry can be observed later than the SOA value. `dhcp_check.py` then tests a reserved client inside an excluded band and an unreserved client, counts offer sources, checks both advertised resolvers and `home`, and verifies A/PTR creation, secondary replication, explicit lease removal, and one minute expiry cleanup. It requires the secondary zone and disables and removes its pilot scope on exit. **Creating a scope with the `set` API enabled allocation immediately in this pilot**; #107 must stage on an isolated path or establish another verified way to prevent household offers before invoking that API.

All three commands require fresh disposable volumes. They print pass/fail evidence without the Site's encrypted operational addresses. The Docker bridge has outbound access for recursion but no published Host ports; the DHCP clients attach only to that bridge. For API or UI inspection, use a temporary container attached **only** to the `technitium-pilot_pilot` network; never publish the management port on a Host interface.

The runner starts a fresh pilot for the API fallback exercise so the restore has no cluster or enabled DHCP scope to disturb.

The pinned [effectivelywild collection v1.1.2](https://github.com/effectivelywild/ansible-collection-technitium-dns/blob/v1.1.2/plugins/module_utils/technitium.py) inserts the API token into URL parameters for reads and JSON requests. That does not meet this plan's bearer-header credential handling. The narrow Python standard-library API path uses bearer headers and passed read/diff, dry run, unchanged second plan, update, backup ZIP integrity, and zone plus disabled-scope restore with its reservation and exclusion. It is pilot code; tickets #105 and #107 still need protected production planning and application.

The runner removes disposable state automatically. If interrupted outside the runner, use `PILOT_PASSWORD_FILE=/tmp/unused docker compose -f services/network/pilot/compose.yaml down -v` to clean up the project.

## Native Pi 3 pilot

The Pi command uses v15.6.2's checksum-verified portable archive and a pinned ASP.NET Core 10.0.13 runtime in the `adam` user's cache. It extracts Debian's `slirp4netns` 1.2.1 and `libslirp0` 4.8.0 into that cache without sudo. The network namespace has outbound IPv4 for independent recursion and no inbound path from the LAN. The command removes its password, configuration, and processes on exit while keeping downloaded binaries for another run.

From the repository root on homelab:

```bash
ssh adam@rasp-pi 'mkdir -p ~/.cache/technitium-pilot-99/scripts'
scp services/network/pilot/pi_check.sh services/network/pilot/pi_inner.sh services/network/pilot/pi_probe.py adam@rasp-pi:.cache/technitium-pilot-99/scripts/
ssh adam@rasp-pi 'bash ~/.cache/technitium-pilot-99/scripts/pi_check.sh'
```

This measures one cold external A query, 50 sequential queries, 800 cached and 800 filtered queries with eight workers, and resident memory. It does not measure a long household soak or the Pi while joined to the cluster.

## Deployment checks after the pilot

The [evidence matrix](evidence.md) records issue #99's acceptance checks and their limits. Before household deployment, join the actual Pi to the DNS Cluster with the approved management and firewall policy, complete a longer capacity soak and promotion/rejoin, and verify reservation hostname precedence, lease renewal, and collision refusal. Design a safe #107 staging path around the observed initial enabled-scope interval. Keep all DHCP tests on a disposable bridge and any restore target unable to answer household DHCP.

## Trusted management HTTPS rehearsal (#100)

Run `services/network/pilot/https_check.sh` from the repository root. It requires Python 3,
OpenSSL 3 and Docker Compose. It creates throwaway CA/leaf material outside Git, configures
both fresh containers with password-protected PKCS#12 certificates, verifies chain, DNS
SAN and expiry checks, then replaces the secondary leaf and verifies the exact new
certificate. All scopes must remain disabled. It refuses existing pilot state and removes
its own containers, volumes and private workspace on exit. This connected rehearsal uses
only disposable keys; the real Site root must be created and kept offline using the
[CA custody guide](../docs/management-https.md).
