# Home Site network

`homelab` runs the Media Stack and a Traefik instance for this Site. `pc` runs Minecraft Bedrock. The Host names are
recorded in this Site Inventory; this initial Site document has no Stack Instances because the Media Stack still uses its
existing Declared Configuration and Traefik is operated as a separate Compose project.

## Name resolution

Technitium DNS and DHCP are configured separately from this change. Create these local DNS records before starting Traefik:

| Name | Record | Destination |
| --- | --- | --- |
| `homelab.home` | A | `homelab`'s LAN address |
| `pc.home` | A | `pc`'s LAN address, once assigned |
| `qbittorrent.home`, `prowlarr.home`, `sonarr.home`, `radarr.home`, `profilarr.home`, `jellyfin.home`, `seerr.home` | CNAME | `homelab.home` |
| The same seven names with `-staging` before `.home` | CNAME | `homelab.home` |
| `minecraft.home` | CNAME | `homelab.home` |

The A records hold the machine addresses for this routing setup. No machine address is stored in this routing project. The
planned Site network inventory will version IP addresses and MAC addresses as SOPS-encrypted values rather than plaintext Git.
Household clients and the Traefik container must resolve these names through Technitium. Configure DHCP to distribute
Technitium as the resolver, and make sure Docker on `homelab` also uses a resolver that knows `homelab.home` and `pc.home`.
If Docker cannot resolve the upstream Host names, Traefik cannot reach the services even when client DNS works.

`pc.home` has no address yet, so Minecraft routing will not be usable until that A record exists.

## Traefik

The reusable [Traefik service](../../services/traefik/compose.yaml) and its [static configuration](../../services/traefik/traefik.yml)
live under `services/traefik`. The [home Site Compose file](traefik/compose.yaml) extends that service and mounts the
Site-owned files in [`traefik/dynamic`](traefik/dynamic). Traefik publishes HTTP on TCP port 80 for the Media Stack
and Bedrock on UDP port 19134 for Minecraft. The dynamic files define the Media Stack HTTP routes and Minecraft UDP route. The routes use
`homelab.home` and `pc.home` as upstream Host names, so moving a service to another machine changes its upstream name or
DNS record without adding an IP to routing configuration.

On `homelab`, from the repository root:

```bash
docker compose -f sites/home/traefik/compose.yaml config
docker compose -f sites/home/traefik/compose.yaml up -d
docker compose -f sites/home/traefik/compose.yaml logs traefik
```

The router for `jellyfin.home`, for example, targets `homelab.home:8096`; `jellyfin-staging.home` targets
`homelab.home:18096`. The qBittorrent routes target Gluetun's published Web UI ports, not a separate qBittorrent network.
If qBittorrent's Host header validation is enabled, add `qbittorrent.home` to Production's server domains and
`qbittorrent-staging.home` to Staging's server domains.
Minecraft clients connect to `minecraft.home` on UDP port 19134, which Traefik forwards to `pc.home:19134`. Allow that UDP
traffic from `homelab` through the PC firewall. UDP does not carry an HTTP hostname, so Minecraft has a dedicated Traefik
entry point and port.

Keep TCP 80 and UDP 19134 reachable only on the trusted LAN; do not forward them from the internet. This configuration has
no TLS, public certificate issuance, dashboard, Docker socket access, or Media Stack Compose labels. The Site Inventory
validation command checks the declared Hosts:

```bash
go run ./cmd/homelab site validate --site home
```
