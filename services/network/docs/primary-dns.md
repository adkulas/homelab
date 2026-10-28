# Primary DNS on homelab — #103

This is the production path for `homelab-dns`, using the pinned image in
`../technitium.env` and `../compose.yaml`. Run commands from the repository root on
**homelab**, at its local console, with a wired recovery client available. Bell keeps
its existing gateway, subnet, DHCP pool and DNS options. Do not start the Pi, join a
cluster, create DHCP scopes or import pilot/restored state during this procedure.

## Behavior and evidence

| Public seam | Required behavior and source | Observable evidence |
| --- | --- | --- |
| `preflight.py --role homelab --deployment` | #103: reject missing routes, unavailable probes, unresolved confirmations and occupied DNS/DHCP/management ports | CLI fixture checks; dated private live report before each change |
| `compose.yaml` | #103: pinned host networking, private persistent state, explicit recursion ACL, independent recursion and private bootstrap | Rendered Compose checks; live settings, DNS and listener checks |
| Local-console procedure | #103 and ADR-0007: preserve Host routes/services, trusted HTTPS, LAN/VPN restrictions, DHCP disabled and timed rollback | Private operator checklist below, including rollback and reboot results |

Automated checks exercise fixture Hosts and render Compose without starting a container.
They do not establish live acceptance. Keep #103 open until the private live checklist
passes; do not treat a merged implementation as a completed household deployment.

## 1. Save and review the Host state — homelab console and recovery PC

Complete setup steps 1, 2, 4 and 5 first. Install Python 3, iproute2, Docker Compose,
Netplan, nftables and `dig`. Validate the encrypted Site Inventory. Open the private
recovery packet; recheck current Bell leases and fixed devices for the selected address.
Record Bell's DHCP-enabled state and verify its pool excludes both static DNS Hosts.
Use an encrypted, mode-0700 directory outside Git as `PACKET_DIR`; disable shell tracing
and use `umask 077`. Save Netplan and the firewall using root at the console:

```bash
sudo cp -a /etc/netplan "$PACKET_DIR/netplan-before"
sudo nft list ruleset > "$PACKET_DIR/firewall-before.nft"
ip -j address > "$PACKET_DIR/addresses-before.json"
ip -j route > "$PACKET_DIR/routes-before.json"
ip -j -6 route > "$PACKET_DIR/routes6-before.json"
docker network inspect $(docker network ls -q) > "$PACKET_DIR/docker-networks-before.json"
```

Before any Host change, run the strict probe (without evidence initially):

```bash
python3 services/network/preflight.py --role homelab --deployment
```

Missing manual confirmations initially produce exit 2. Resolve every observed port or
route conflict and every unavailable probe before proceeding; confirmations cannot waive
these findings. The automated route check compares IPv4 prefixes on default-route
interfaces with Docker IPAM. Review Wi-Fi fallback, VPN/custom routing tables and IPv6
separately; the automated check cannot establish that those paths are correct.

Inspect the results privately. Compare LAN and Docker subnets for overlap, preferred
Ethernet and fallback Wi-Fi default routes, route metrics, gateway, IPv6 advertisements
and Tailscale routes. Inspect `ss -H -lntu`, Docker published ports and
`resolvectl status`. A listener on loopback 53 (including systemd-resolved's stub)
conflicts with Technitium's wildcard DNS listener. Do not disable the Host resolver
without a separately reviewed replacement `/etc/resolv.conf` and a saved rollback copy.
Stop on conflicts; the strict preflight deliberately cannot waive them.

From the recovery client, record baseline gateway/internet, SSH, Docker/Media Stack,
Traefik, Minecraft forwarding, cameras/NVR and VPN checks relevant to this Site.
Save exact URLs/ports and expected outcomes privately so these checks can be repeated.

## 2. Prepare static addressing and a timed rollback — homelab console

**Interruption:** This step may disconnect SSH. Stay at the tested local console.
Copy the saved Netplan directory to a second private working directory. Edit the
existing Ethernet stanza in that copy using the validated encrypted Site values:
replace its DHCPv4 address with the selected Host address/prefix, retain Bell's gateway
as the default route, and retain the existing route priority. Keep Wi-Fi credentials,
Wi-Fi DHCP and fallback metric, IPv6 policy, renderer, interface matching and all other
settings. Keep the Host's bootstrap DNS independent of the new resolver. Review the
complete profile against the original, not just the changed lines. No generic profile
can safely replace the Host's existing Wi-Fi configuration.

Create the following rollback script beside the saved `netplan-before` directory.
It removes this attempt's YAML files before restoring the complete prior directory.
Keep both on local encrypted storage available without the network, root-owned and
unwritable by other users. This script is for the tested local console only:

```bash
sudo tee "$PACKET_DIR/rollback-netplan.sh" >/dev/null <<'SH'
#!/bin/bash
set -euo pipefail
packet_dir=$(dirname -- "$(readlink -f -- "$0")")
[[ -d "$packet_dir/netplan-before" ]]
shopt -s nullglob
saved=("$packet_dir"/netplan-before/*.yaml)
(( ${#saved[@]} > 0 ))
current=(/etc/netplan/*.yaml)
if (( ${#current[@]} > 0 )); then
  rm -- "${current[@]}"
fi
cp -a -- "$packet_dir/netplan-before/." /etc/netplan/
netplan generate
netplan apply
SH
sudo chown root:root "$PACKET_DIR/rollback-netplan.sh"
sudo chmod 0700 "$PACKET_DIR/rollback-netplan.sh"
sudo bash -n "$PACKET_DIR/rollback-netplan.sh"
```

Inspect the saved profiles and script before arming it. Use a five-minute systemd timer
so recovery survives an SSH disconnect:

```bash
sudo systemd-run --unit=homelab-dns-address-rollback --on-active=5m \
  /bin/bash "$PACKET_DIR/rollback-netplan.sh"
```

Install only the reviewed candidate YAML files under `/etc/netplan/`, root-owned mode
0600, then run `sudo netplan generate` followed by `sudo netplan try --timeout 120`.
Confirm at the console only after the wired client verifies gateway, internet, SSH and
all baseline services. On failure let Netplan revert; the five-minute timer restores
both the persistent files and effective state. Do not proceed until rollback is tested.
After success, cancel the timer with
`sudo systemctl stop homelab-dns-address-rollback.timer`, then reboot and repeat the
checks, including Wi-Fi fallback and default-route priorities. Re-arm rollback before
any further Host profile change. Bell's LAN and DHCP settings remain untouched.

## 3. Install access rules before starting DNS — homelab console

Integrate these rules into the Host's **existing persistent firewall**, preserving SSH,
VPN and Docker forwarding. Save the prior firewall and its persistence configuration.
Use the approved LAN/VPN source ranges from private inventory, separately for DNS clients
and management administrators. A source being RFC1918 is not sufficient authorization.

| Input path | Required policy |
| --- | --- |
| Loopback | Permit the bootstrap HTTP console TCP 5380 |
| Approved DNS clients | Permit UDP and TCP 53 |
| Approved administrators and future DNS peer | Permit TCP 53443 |
| Every non-loopback source | Deny TCP 5380 and UDP 67 |
| All other sources | Deny UDP/TCP 53 and TCP 53443, on IPv4 **and IPv6** |

Use an inet input chain or equivalent dual-stack rules. Keep the UDP 67 deny even
though no scope is created. Do not add NAT, WAN forwards, Docker socket mounts or
optional encrypted DNS listeners. Inspect every effective input chain: a rule allowing
traffic in one nftables base chain cannot override a later chain dropping it. Conversely,
UFW rules for published Docker ports are not proof for this host-networked service.

Validate your private rules file with `sudo nft --check --file "$FIREWALL_FILE"` before
loading it through the existing firewall manager. Verify approved and denied source
paths from separate clients; record the rule counters and results privately. Configure
persistence to load these restrictions **before Docker starts**, and fail closed if
loading fails. Reboot and inspect the effective rules before continuing. Do not proceed
with temporary rules only. Retain exact restore commands for the existing firewall
manager in the packet; restoring a saved nft ruleset alone may not restore its boot policy.

## 4. Prepare fresh private state and bootstrap — homelab console

Choose absolute paths outside Git on encrypted storage as `PRIMARY_CONFIG_DIR` and
`PRIMARY_PASSWORD_FILE`. Create a new empty configuration directory, root-owned mode
0700. Create the administrator password file mode 0600 using a private password manager
or hidden prompt, not a command-line argument. Export only these **paths** and the
comma-separated approved networks as `PRIMARY_RECURSION_ACL`; never export the password.
The ACL must include only the verified LAN/VPN clients (and approved IPv6 networks if
used). Loopback is allowed by Technitium. Do not use a wildcard allow-all network.

The environment initializes **fresh state only**. Subsequent Compose edits do not
reconcile saved Technitium settings. Never reuse pilot data, restore files or a directory
containing scopes, even if you intend to disable DHCP after start.

Run the read-only strict gate initially without evidence to list confirmations:

```bash
python3 services/network/preflight.py --role homelab --deployment
```

Use the private boolean evidence file from setup step 4. Add the deployment confirmations
only after verifying their dated evidence: routes/Docker overlap, persistent firewall,
armed rollback/local console, and fresh configuration. Occupied ports or failed probes
remain blockers regardless of these booleans.

Define the Compose command for this shell:

```bash
primary_compose() {
  docker compose --env-file services/network/technitium.env \
    -f services/network/compose.yaml "$@"
}
primary_compose config --quiet
primary_compose pull
```

Review image/digest and private mount paths locally. Arm a separate five-minute service
rollback before first start; `docker compose stop` disables automatic restart of this
container until explicitly started again:

```bash
sudo systemd-run --unit=homelab-dns-service-rollback --on-active=5m \
  /usr/bin/docker stop homelab-dns-primary-1
python3 services/network/preflight.py --role homelab --deployment --evidence "$EVIDENCE" \
  && primary_compose up -d
```

Run that final command immediately after the gate; exit 2 means stop. Start no other DNS
Host. If verification needs longer, let the timer stop the service, resolve the issue,
then re-arm before another supervised start. For unrelated image pull or Docker errors,
stop and inspect locally; do not weaken the gate or paste private logs into GitHub.

From the recovery PC open an SSH tunnel to the privately verified Host address:
`ssh -L 15380:127.0.0.1:5380 "$HOMELAB_SSH"`. Browse `http://127.0.0.1:15380`, log in with
the file-backed administrator password, and confirm DHCP has **no scopes**.

## 5. Install trusted HTTPS and verify service — console and recovery client

Follow the management HTTPS guide to install this Host's issued PFX privately under
`/etc/dns/tls/management.pfx` (the private bind mount's `tls` directory). In the tunneled
Settings / Web Service console, set port 53443, enable HTTPS, disable self-signed TLS,
and enter the certificate path/password. Add the verified Host LAN address and approved
VPN address, if used, to Web Service local addresses alongside loopback. This setting
controls both HTTP and HTTPS bindings: the firewall must keep non-loopback 5380 blocked.
Apply and verify the trusted direct HTTPS endpoint before enabling HTTP-to-TLS redirect.
Never place the PFX password in Compose or shell arguments.

In Settings / Recursion confirm `UseSpecifiedNetworkACL` and the exact approved ranges;
in Settings / Proxy & Forwarders confirm no proxy and **no forwarders**. Enable DNSSEC
validation under Settings / General if it is not already enabled. In Zones create a
Primary zone `deployment-test.home`, with a single A record named `probe` pointing to
the validated primary Host address. This temporary test zone does not establish Git
ownership of `home` or the reverse zone; #105 owns those changes. Set no DHCP scope.

Run from the wired client (variables are privately read from inventory):

```bash
dig @"$RESOLVER_ADDRESS" probe.deployment-test.home A
dig +tcp @"$RESOLVER_ADDRESS" probe.deployment-test.home A
dig @"$RESOLVER_ADDRESS" "$FRESH_EXTERNAL_NAME" A
python3 services/network/ca/certificates.py probe --name homelab.home \
  --address "$RESOLVER_ADDRESS" --ca-cert "$ROOT_CERT"
```

Confirm the exact local address, successful external answer with recursion available,
trusted chain/name and expiry, and browser trust without an exception. Clear the resolver
cache using its console, then query a known resolvable external name not previously
queried. Keep the Pi stopped: confirm no forwarders/peer dependency. Check DNSSEC with
a current signed domain (validated result) and a known deliberately invalid signed domain
(SERVFAIL with validation enabled). Record selected names and results privately.

From approved LAN/VPN clients verify DNS and HTTPS; from an unapproved source verify both
are denied. Check IPv4 and IPv6, including HTTP denial. Inspect `ss -H -lntu` and the DHCP
page: no UDP 67 listener and no scopes. Renew an isolated wired test client's Bell lease;
confirm Bell remains the only offer source using the existing packet-capture procedure,
with unchanged gateway and DHCP options. Re-run every baseline household service check.
Delete only `deployment-test.home` after the probes; #105 will declare permanent records.

After these checks pass, cancel the service timer:
`sudo systemctl stop homelab-dns-service-rollback.timer`. Reboot homelab under supervision,
verify firewall-before-Docker ordering and repeat DNS/HTTPS/DHCP/service checks. Do not
re-run the first-start gate while DNS is running: occupied ports are then expected.
Take a protected backup using the backup guide before any subsequent configuration change.

## 6. Rollback and acceptance record

On any failure, run `primary_compose stop primary` locally (or allow the armed timer).
Verify the new DNS/management listeners and UDP 67 are absent. Return the manually
configured test client to its prior Bell DNS configuration; Bell DHCP was never changed.
Run `primary_compose down` to remove only this Compose project, retaining the private
configuration/password for diagnosis; do not prune volumes or affect other projects.
Restore the original firewall through its manager after the container is stopped. Restore
the saved Netplan files using the tested local rollback script if addressing changed,
then verify gateway, DNS, SSH, Docker and household services. Keep Bell's subnet/gateway.

In the private packet record operator/date, commit/image digest, preflight report,
Netplan rollback and reboot, permitted/denied dual-stack probes, local/uncached/DNSSEC
answers, trusted HTTPS, zero Technitium scopes/offers, unchanged household services and
successful service/address removal rehearsal. Publish only a redacted pass/fail summary.
Until these live results exist, the implementation is ready for supervised deployment;
#103's live acceptance remains pending.

Pinned vendor references: [Compose](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/docker-compose.yml)
and [first-start environment variables](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/DockerEnvironmentVariables.md).
