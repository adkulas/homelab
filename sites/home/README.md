# Home Site network

`homelab` runs the Media Stack and a Traefik instance for this Site. `pc` runs Minecraft Bedrock. The Site Inventory now
declares two future DNS Stack Instances and their DNS Cluster: `homelab-dns` on `homelab` and `raspberrypi-dns` on
`raspberrypi`. These declarations do not deploy DNS. The Media Stack still uses its existing Declared Configuration and
Traefik remains a separate Compose project. The [topology guide](docs/topology.md) records how to add confirmed encrypted
addresses and reservations before deployment.

Follow the [ordered network setup and recovery walkthrough](../../services/network/docs/setup.md) before any live network change.

The `homelab` Host's [provisioning guide and Ansible playbook](hosts/homelab/README.md)
manage its operating system, container runtime, and the Site Traefik deployment.
Media application reconciliation remains with the Media Stack CLI.

## Name resolution

Technitium DNS and DHCP are configured separately from this change. Create these local DNS records before starting Traefik:

| Name | Record | Destination |
| --- | --- | --- |
| `homelab.home` | A | `homelab`'s LAN address |
| `pc.home` | A | `pc`'s LAN address, once assigned |
| `qbittorrent.home`, `prowlarr.home`, `sonarr.home`, `radarr.home`, `profilarr.home`, `jellyfin.home`, `seerr.home` | CNAME | `homelab.home` |
| The same seven names with `-staging` before `.home` | CNAME | `homelab.home` |
| `minecraft.home` | CNAME | `homelab.home` |
| `technitium.home`, `technitium-pi.home` | CNAME | `homelab.home` |

The A records hold the machine addresses for this routing setup. No machine address is stored in this routing project. The
Site network inventory versions proposed IP addresses and MAC addresses as SOPS-encrypted values rather than plaintext Git.
The [Bell device observation](secrets/devices.sops.yaml) is a SOPS-encrypted snapshot from the router dashboard and DHCP
lease table, updated with later aliases and address changes. It contains connected devices and historical entries that overlap
proposed infrastructure addresses. It is not a complete client export, a reservation list, or proof that an address is
available. It uses the existing repository age recipient, also used by the proposed [encrypted topology](secrets/site.sops.yaml).
The operator keeps copies of this one identity on three machines; those copies do not create a second recipient. Edit the
snapshot with `sops sites/home/secrets/devices.sops.yaml`; commit only the encrypted file.
Plain YAML files in `secrets/` are ignored.
The same encrypted snapshot records the operator-confirmed current Bell DHCP pool and lease duration. The revised pool begins
above the proposed infrastructure address band. Historical low-address entries still require a live conflict check before
assigning static Host addresses. The wired Hosts, NVR, and Hue bridge were reconfirmed in the revised wired dashboard.
Household clients and the Traefik container must resolve these names through Technitium. Configure DHCP to distribute
Technitium as the resolver, and make sure Docker on `homelab` also uses a resolver that knows `homelab.home` and `pc.home`.
If Docker cannot resolve the upstream Host names, Traefik cannot reach the services even when client DNS works.

`pc.home` has no address yet, so Minecraft routing will not be usable until that A record exists.

## Traefik

The reusable [Traefik service](../../services/traefik/compose.yaml) and its [static configuration](../../services/traefik/traefik.yml)
live under `services/traefik`. The [home Site Compose file](traefik/compose.yaml) extends that service and mounts the
Site-owned files in [`traefik/dynamic`](traefik/dynamic). Traefik publishes HTTP on TCP port 80 for the Media Stack
and Bedrock on UDP port 19134 for Minecraft. The dynamic files define the Media Stack HTTP routes, Technitium management
redirects and Minecraft UDP route. The routes use
`homelab.home` and `pc.home` as upstream Host names, so moving a service to another machine changes its upstream name or
DNS record without adding an IP to routing configuration.

Deploy from the controller checkout using the Host playbook's `traefik` tag;
see the [deployment and verification workflow](hosts/homelab/README.md#traefik-deployment).
Ansible copies the selected files to root-owned `/opt/homelab` on homelab and
manages the single `home-traefik` project there. Use that installed directory for
read-only Compose diagnostics. Avoid starting another project from a working
checkout: it would compete for the same identity and published ports.

The router for `jellyfin.home`, for example, targets `homelab.home:8096`; `jellyfin-staging.home` targets
`homelab.home:18096`. The qBittorrent routes target Gluetun's published Web UI ports, not a separate qBittorrent network.
If qBittorrent's Host header validation is enabled, add `qbittorrent.home` to Production's server domains and
`qbittorrent-staging.home` to Staging's server domains.

`http://technitium.home` redirects to the primary console at `https://homelab.home:53443`;
`http://technitium-pi.home` redirects to the secondary console at `https://raspberrypi.home:53443`.
Both aliases point to `homelab`, where Traefik runs. Reconcile the aliases from `network.json`
before using them. Redirects preserve paths and query strings and keep management traffic on
Technitium's existing HTTPS listeners with their Host certificates. Administrators still need
to trust the [home CA](../../services/network/docs/management-https.md) and have access through
the existing management firewall rules.

Houndarr and Cleanuparr are administered through Host loopback ports and have no Traefik routes or DNS records.
Production uses ports 8877 and 11011; Staging uses 18877 and 21011. Use the SSH tunnel described in the
[Media Stack companion guide](../../services/media/README.md#installation-and-automatic-first-run) for remote access.

Minecraft clients connect to `minecraft.home` on UDP port 19134, which Traefik forwards to `pc.home:19134`. Allow that UDP
traffic from `homelab` through the PC firewall. UDP does not carry an HTTP hostname, so Minecraft has a dedicated Traefik
entry point and port.

Keep TCP 80 and UDP 19134 reachable only on the trusted LAN; do not forward them from the internet. This configuration has
no TLS, public certificate issuance, dashboard, Docker socket access, or Media Stack Compose labels. The Site Inventory
validation command checks the declared Hosts:

```bash
go run ./cmd/homelab site validate --site home
```
