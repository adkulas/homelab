# Home Media Stack

The Home Media Stack is a reproducible, self-hosted system for requesting, acquiring, organizing, and playing movies and
episodic television. It is designed for Ubuntu and Docker Desktop with WSL 2, confines torrent traffic to NordVPN, and keeps
independent Production and Staging Environments so changes can be proven before they affect the household libraries.

> [!IMPORTANT]
> This stack is being implemented incrementally. `init`, `doctor`, the current Compose-rendering form of `plan`, the
> VPN-confined qBittorrent, Radarr Movie Library, Sonarr Series Library, Prowlarr Movie and Series Library discovery, and guided Profilarr connection phases of `apply`; authenticated Jellyfin Movie and Series Library reconciliation; VPN-focused
> `verify --suite full`; legal movie and series-episode acquisition, hardlink, and playback proofs through `verify --suite promotion`; and verified application-state archives through `backup` are available now.
> Remaining Seerr reconciliation, `restore`, the other verification suites and
> artifacts, and `destroy` remain planned. Ubuntu is the authoritative implementation and
> completion platform. Docker
> Desktop WSL2 compatibility is verified independently and does not block Ubuntu work. Sections below label planned behavior explicitly.
> The authoritative target design is in the
> [implementation plan](../../docs/plans/media-stack.md) and
> [CLI contract](../../docs/plans/media-stack-cli.md).

## What the stack provides

- Jellyfin playback for the Movie Library and Series Library.
- Seerr requests with Jellyfin household authentication and approval by default.
- Radarr and Sonarr library management.
- Prowlarr discovery through an explicit approved list of Public Torrent Sources.
- qBittorrent acquisition through a fail-closed Gluetun and NordVPN boundary.
- Profilarr-managed, pinned TRaSH-derived quality and naming policy.
- One shared `/data` filesystem seam so completed downloads can be imported as hardlinks instead of copied.
- A Go CLI for initialization, preflight checks, reconciliation, verification, backups, restoration, and safe Promotion.
- Completely isolated Production and Staging Environments.

Every web interface is intended for the trusted LAN and requires authentication. Nothing is intentionally exposed to the
public internet.

## Quick start

### 1. Prepare a supported host

Use either:

- Ubuntu 22.04, 24.04, or 26.04 with Docker Engine and the Docker Compose plugin; or
- Windows with Docker Desktop using its WSL 2 backend, running all repository commands from an integrated Ubuntu WSL
  distribution.

On Ubuntu, install the base tools and `age` first:

```bash
sudo apt update
sudo apt install ca-certificates curl git age
```

Install Docker Engine from the [official Ubuntu apt-repository instructions](https://docs.docker.com/engine/install/ubuntu/).
The package step must include all of the components used by this stack:

```bash
sudo apt install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
docker version
docker compose version
```

For Windows, install [Docker Desktop with its WSL 2 backend](https://docs.docker.com/desktop/features/wsl/), enable
**Settings > Resources > WSL Integration** for the Ubuntu distribution, and install `git`, `curl`, and `age` inside that
distribution with the Ubuntu command above. Do not install a second Docker Engine inside the integrated distribution.

Install SOPS inside Ubuntu or the integrated WSL distribution. This amd64 example pins the version documented here; use the
matching `linux.arm64` release asset on arm64:

```bash
curl -LO https://github.com/getsops/sops/releases/download/v3.13.2/sops-v3.13.2.linux.amd64
curl -LO https://github.com/getsops/sops/releases/download/v3.13.2/sops-v3.13.2.checksums.txt
sha256sum --ignore-missing -c sops-v3.13.2.checksums.txt
sudo install -m 0755 sops-v3.13.2.linux.amd64 /usr/local/bin/sops
rm sops-v3.13.2.linux.amd64 sops-v3.13.2.checksums.txt
sops --version
age --version
```

Generate an age identity once and keep the identity file outside Git. Give the resulting `age1...` recipient to `init`:

```bash
mkdir -p ~/.config/sops/age
age-keygen -o ~/.config/sops/age/keys.txt
age-keygen -y ~/.config/sops/age/keys.txt
```

You also need:

- a native Linux or WSL filesystem for active downloads and libraries;
- separate, access-controlled Production and Staging backup locations, preferably on another disk or NAS;
- `/dev/net/tun` and permission for Docker to grant Gluetun `NET_ADMIN`; and
- NordVPN OpenVPN **service credentials** from the Nord Account manual-setup area. These are not the Nord account email and
  password, and the stack does not need a Nord access token.

### 2. Configure and initialize Staging first

Edit `stacks/media/media-stack.yaml` before the first run. At minimum, choose absolute, non-overlapping `dataRoot` and
`backupRoot` values, Compose `projectName` values, secret-file paths, and non-conflicting LAN ports for Production and
Staging. Each `backupRoot` must also be outside its Environment's `dataRoot`. `dataRoot` is not prompted by `init`; it is a declared path in the checked-in configuration, and `init` provisions it.
The `backup` command creates `backupRoot` with private permissions. Set `runtimeUID` and `runtimeGID` to the numeric
identity that should own new data directories; for the current operator, obtain them with:

```bash
id -u
id -g
```

From the repository root, initialize Staging:

```bash
go build -o bin/media-stack ./cmd/media-stack
./bin/media-stack init --environment staging
```

That local build path is the simplest way to start a fresh implementation from the current repository state.

If you prefer the pinned release launcher, `setup.sh` downloads the pinned `media-stack` Linux binary for amd64 or arm64, verifies its SHA-256 checksum, caches it, and runs `media-stack init`. `init` currently prompts for the runtime UID/GID, timezone, NordVPN country, optional P2P category,
OpenVPN UDP or TCP, Gluetun catalogue interval, hardware transcoding preference, age recipient, and NordVPN service credentials. Non-interactive automation can
supply the same answers explicitly:

```bash
media-stack init \
  --environment staging \
  --non-interactive \
  --answers staging-answers.yaml
```

Initialization also asks for a unique, high-entropy Profilarr API key of at least 32 characters and distinct Jellyfin administrator credentials for the selected Environment. The Profilarr key lets the CLI use
Profilarr's documented API to verify the one-time guided Radarr and Sonarr connections; use a different value for each
Environment. The Jellyfin credentials bootstrap or authenticate the selected Environment through its supported API and are used for Movie and Series Library playback verification. Initialization writes the reviewable Declared Configuration and `secrets/staging.sops.yaml`; it never starts containers. It
also creates only the selected Staging Environment layout:

```text
<dataRoot>/
├── torrents/
│   ├── movies/
│   └── series/
└── media/
    ├── movies/
    └── series/
```

Every directory created by `init` uses mode `0750` and the declared runtime UID/GID. Assigning an identity other than the
operator may therefore require pre-provisioned permissions or a privileged invocation. Existing directories and their
contents are left untouched: `init` does not recursively `chown` or `chmod` data. Re-running restores missing derived
directories while preserving complete configuration and encrypted secrets.

### 3. Check prerequisites and render the selected topology

These commands are available now:

```bash
media-stack doctor --environment staging
media-stack doctor --environment staging --output json
media-stack plan --environment staging > staging-compose.yaml
docker compose -f staging-compose.yaml config --quiet
```

`doctor` checks the supported Linux/WSL platform, Docker Engine, Compose, SOPS, age, decryption of the selected Environment
secret, `/dev/net/tun`, `NET_ADMIN`, and whether the pinned Gluetun catalogue accepts the declared NordVPN filters. For
`hardwareTranscoding: auto`, it reports a missing render node as an optional skip and uses the pinned Jellyfin image to
prove that a detected node is usable; invalid devices or container access fail with a remedy. A disabled preference is reported as an explicit skip. Doctor also runs disposable probes from the pinned qBittorrent, Radarr, and Sonarr images as the declared runtime UID/GID, using their
actual `/data/torrents` or `/data` bind-mount shapes. Those probes verify write permission, same-device layout, hardlink
creation and inode identity, atomic rename, filesystem events, and cleanup. Human output explains each result; JSON provides
stable diagnostic records and remedies for automation. Each disposable Docker probe has a two-minute deadline and disables
the pinned image's service health check so a quick preflight command cannot be retained by Docker Desktop after it exits. It
does not start the stack.

The current `plan` implementation validates Declared Configuration and pinned images, then writes the selected Environment
Compose model to standard output. It is useful for reviewing project scoping, ports, runtime identity, mounts, secrets,
restart policy, bounded logging, and optional hardware transcoding. An Environment with `hardwareTranscoding: auto` receives
the `/dev/dri/renderD128` Jellyfin device mapping and the host render device's numeric group ID when that Linux DRM render
node is available. Set `hardwareTranscoding: disabled` to retain the portable base topology even on a supported host.
Unsupported hosts also retain the portable topology without a device mapping or supplemental group.

Validate hardware acceleration on the actual target host after applying the rendered topology:

```bash
test -c /dev/dri/renderD128
media-stack doctor --environment staging
media-stack plan --environment staging > staging-compose.yaml
docker compose -f staging-compose.yaml config --quiet
media-stack apply --environment staging
docker compose -f staging-compose.yaml exec jellyfin \
  /usr/lib/jellyfin-ffmpeg/vainfo --display drm --device /dev/dri/renderD128
```

The `vainfo` command must list the host's supported codec profiles without a device or permission error. In Jellyfin's
Dashboard, open **Playback > Transcoding**, select VA-API with `/dev/dri/renderD128`, and enable only profiles reported by
`vainfo`. Finally, force a transcode from a client by selecting a lower playback quality; the active session must report
transcoding and its FFmpeg log must show the VA-API device. This live check is required because schema-valid Compose proves
the device contract, but only the target GPU, kernel, and driver can prove codec execution.

`plan` does not yet observe applications, create a saved Plan Artifact, or mutate the host.
Shell redirection is used above because `--out` is not implemented yet.

### 4. Start VPN-confined qBittorrent and prepare library discovery

After `doctor` passes, start Staging qBittorrent behind Gluetun:

```bash
media-stack apply --environment staging
```

The currently available `apply` phase decrypts the selected Environment's NordVPN OpenVPN service credentials, Profilarr API key, and Jellyfin administrator credentials. Only the OpenVPN values and Profilarr key are materialized as runtime files; Jellyfin credentials remain in memory. It atomically materializes them as `0600` files beneath the operator's `0700`
`$XDG_RUNTIME_DIR/media-stack/<projectName>/` directory, streams the value-free Compose model to Docker, starts
Gluetun, and waits up to two minutes for its built-in health check. Only after Gluetun is healthy does `apply` start
qBittorrent, which shares Gluetun's network namespace. It then reconciles qBittorrent through its Web API: the default
save path is `/data/torrents`; Automatic Torrent Management relocates downloads when their torrent, save path, or category
changes; the exact `movies` and `series` categories use relative `movies` and `series` save paths; and global seeding stops
at ratio 1.0 or seven days, whichever is reached first. Repeating `apply` observes those values and makes no API writes when
they already match the Declared Configuration. During the CLI-owned bootstrap phase, `apply` reads the pinned LinuxServer
image's latest temporary admin password from container logs, exchanges it for an in-memory Web API session, and never writes
the password to command output or repository files. No remote path mappings are created. A transient unhealthy state is polled so Gluetun can try
another catalogue endpoint; a persistent failure exits `1`. The runtime files remain while the service is running so Docker
can remount them after a container restart. After qBittorrent converges, `apply` starts Radarr, reads its generated API key
from that Environment's `/config/config.xml` into memory, and waits for the supported Radarr v3 API. It reconciles
`/data/media/movies` as the Movie Library root and a qBittorrent download client at `qbittorrent:8080` with the `movies`
category. It then starts Sonarr, reads its generated API key from the selected Environment's `/config/config.xml`, and waits
for its supported v3 API. It reconciles `/data/media/series` as the Series Library root and a qBittorrent download client at
`qbittorrent:8080` with the `series` category. qBittorrent and both Arr applications see the same `/data` paths, so no remote
path mapping is created. Repeated reconciliation makes no API writes once these values match. It then starts API-ready
Prowlarr and reconciles the checked-in `internetarchive`
Public Torrent Source plus full-sync Radarr and Sonarr application links. Prowlarr reaches Radarr at `http://radarr:7878`
and Sonarr at `http://sonarr:8989`; both links use the `http://prowlarr:9696` callback URL. The Sonarr link syncs the pinned
standard and anime TV categories reported by the supported Prowlarr schema. The Internet Archive source is the first entry
in the CLI's approved semantic catalog, requires no credentials, and supports legal movie and series searches. Repeating
`apply` observes all three Prowlarr resources through `/api/v1` and makes no writes when they already match. Rerunning `apply` safely replaces the
runtime VPN files and converges Gluetun, qBittorrent, Radarr, Sonarr, and Prowlarr.
Gluetun alone
publishes the selected Environment's qBittorrent Web UI port, and its firewall admits only the narrow input port `8080`
needed for that UI. Its `qbittorrent` application-network alias preserves the internal
`http://qbittorrent:8080` endpoint without granting qBittorrent an independent route. Later tickets extend `apply` to the
other services and application reconciliation.

### 5. Verify Staging VPN behavior and a legal movie import

```bash
media-stack verify --environment staging --suite full
media-stack verify --environment staging --suite full --output json
media-stack verify \
  --environment staging \
  --suite promotion \
  --legal-fixture stacks/media/fixtures/legal-movie.yaml \
  --legal-series-fixture stacks/media/fixtures/legal-series.yaml
```

The available full-suite phase verifies the Gluetun TUN device and health, resolves an external-IP service from
qBittorrent's shared namespace, and requires that tunneled address to differ from the host address. It then stops Gluetun,
requires a fresh qBittorrent-namespace request to fail, restarts Gluetun, waits up to two minutes for health, recreates
qBittorrent in Gluetun's current network namespace, and proves tunneled egress returns. Cleanup always attempts recovery after an interruption. Run it only in the
Staging Environment because it deliberately interrupts acquisition. Exit `1` means verification failed; JSON output
includes stable `VERIFY_*` diagnostics and remedies.

The available Promotion phase reads either or both explicit legal fixtures. It uses Radarr or Sonarr to register the declared
item, searches the linked Internet Archive source, and grabs only the exact declared release. It waits for the corresponding
`movies` or `series` qBittorrent torrent to complete and for the Arr application to expose the imported file, then compares
the source and library filesystem identities beneath the selected Environment `dataRoot`. The `VERIFY_MOVIE_*` and
`VERIFY_SERIES_EPISODE_*` diagnostics pass only when each requested path succeeds and both paths resolve to the same inode.
For either fixture, Promotion then authenticates to Jellyfin, waits for the exact imported path to appear, and requires direct-play readiness through playback information; `VERIFY_MOVIE_PLAYBACK_READY` or `VERIFY_SERIES_EPISODE_PLAYBACK_READY` records success. The checked-in fixtures allow up to 45 minutes for download, import, and playback discovery. Run Promotion verification only in a disposable
Staging Environment; it adds the fixture items and retains their qBittorrent sources for seeding.

The smoke and Restore Drill suites and content-addressed Verification Artifacts remain planned.

### 6. Create Production and promote the proven change (planned)

Initialize Production separately, then use the Staging evidence when planning the Production change:

```bash
media-stack init --environment production
media-stack doctor --environment production
media-stack plan \
  --environment production \
  --verified-by staging-verification.json \
  --out production-plan.json
media-stack apply --plan production-plan.json
```

Production is never the default. A changed configuration, image digest, CLI contract, expired verification, or missing current
backup when one is required invalidates Promotion evidence.

## Target system design (planned)

### Request-to-playback flow

```mermaid
flowchart LR
    User[Household user] -->|request| Seerr
    User -->|play| Jellyfin

    Seerr -->|movie request| Radarr
    Seerr -->|series request| Sonarr
    Radarr -->|search| Prowlarr
    Sonarr -->|search| Prowlarr
    Prowlarr -->|query| Source[Approved Public Torrent Source]

    Radarr -->|movies category| QB[qBittorrent]
    Sonarr -->|series category| QB
    QB -->|shared network namespace| Gluetun
    Gluetun -->|OpenVPN| NordVPN
    NordVPN --> Internet[Internet]

    QB -->|download| Torrents["/data/torrents"]
    Torrents -->|hardlink import| Libraries["/data/media"]
    Libraries -->|read only| Jellyfin

    Profilarr -->|quality and naming policy| Radarr
    Profilarr -->|quality and naming policy| Sonarr
```

Seerr turns approved household requests into Radarr or Sonarr searches. Prowlarr supplies releases from explicitly approved
sources. Radarr and Sonarr submit categorized downloads to qBittorrent and hardlink completed files into the appropriate
library. Jellyfin scans the library through a read-only mount and serves it to household users.

### VPN confinement and internal addressing

qBittorrent has no network attachment of its own. It uses `network_mode: service:gluetun`, so the two containers share a
network namespace and Gluetun's firewall is qBittorrent's only egress path.

```mermaid
flowchart TB
    subgraph Project[Environment Compose project]
        Apps[Radarr / Sonarr] -->|http://qbittorrent:8080| Alias[qbittorrent network alias]

        subgraph Namespace[Gluetun network namespace]
            Alias --> QB[qBittorrent :8080]
            QB --> Firewall[Gluetun firewall]
            Firewall --> Tunnel[OpenVPN tunnel]
        end
    end

    Tunnel --> NordVPN[NordVPN]
    NordVPN --> Internet[Internet]
    Firewall -. tunnel unhealthy .-> Blocked[egress blocked]
```

Gluetun, not qBittorrent, publishes the qBittorrent Web UI port. Gluetun also carries the `qbittorrent` alias on the
application network, preserving the stable internal address `http://qbittorrent:8080` for Radarr and Sonarr without giving
qBittorrent another route. The deployed design prohibits host networking, privileged mode, `FIREWALL=off`, and broad LAN
firewall exceptions as kill-switch workarounds.

`media-stack verify --environment staging --suite full [--config path] [--output human|json]` compares the public IP
seen from qBittorrent's shared namespace with a host-side observation, interrupts the tunnel, proves a fresh egress attempt
fails instead of using the host route, then restores Gluetun and verifies tunneled egress returns.
`--suite promotion` runs that proof first, then drives each supplied `--legal-fixture <path>` and
`--legal-series-fixture <path>` release through its Arr application and qBittorrent and requires source/imported inode
equality. At least one fixture is required. Both suites reject Production because they are disruptive.

### Storage and hardlinks

Each environment owns one data root with a stable in-container `/data` mapping:

```text
<data-root>/
├── torrents/
│   ├── movies/
│   └── series/
└── media/
    ├── movies/
    └── series/
```

| Service | Container mount | Access and purpose |
| --- | --- | --- |
| qBittorrent | `/data/torrents` | Writes categorized downloads |
| Radarr | `/data` | Hardlinks movies from torrents into the Movie Library |
| Sonarr | `/data` | Hardlinks episodes from torrents into the Series Library |
| Jellyfin | `/data/media` | Read-only library access |

Because all paths are below one filesystem seam, Radarr and Sonarr can create hardlinks. A torrent may continue seeding while
the library entry exists under its organized name without consuming space for a second copy. When qBittorrent later removes
its torrent data, the library hardlink remains.

`doctor` checks this behavior from disposable containers running as the declared application UID/GID. It verifies writable
paths, same-device layout, hardlink creation and inode equality, atomic rename, filesystem events, permissions, and cleanup.
Native Windows/NTFS data paths are experimental and remain unavailable unless every container-observable probe passes.

### Production and Staging isolation

```mermaid
flowchart LR
    Config[Reviewed configuration] --> Staging[Staging Environment]
    Staging -->|verify exact change| Evidence[Verification Artifact]
    Evidence -->|Promotion gate| Production[Production Environment]

    subgraph StagingBoundary[Isolated Staging resources]
        Staging --> StagingCompose[Compose project]
        Staging --> StagingPorts[LAN ports]
        Staging --> StagingSecrets[encrypted secrets]
        Staging --> StagingData[data and backups]
    end

    subgraph ProductionBoundary[Isolated Production resources]
        Production --> ProductionCompose[Compose project]
        Production --> ProductionPorts[LAN ports]
        Production --> ProductionSecrets[encrypted secrets]
        Production --> ProductionData[data and backups]
    end
```

The environments have distinct project names, ports, networks, volumes, secrets, API keys, data roots, backup namespaces,
and full service sets. Staging can perform real acquisition and recovery exercises without sharing state, scheduled jobs, or
integrations with Production.

Compose project scoping supplies resource names; fixed `container_name` values and shared global networks are deliberately
forbidden. Promotion means applying the exact configuration and image-version change already verified in Staging—not simply
rerunning similar commands in Production.

## Services

| Service | Responsibility | Default example ports¹ |
| --- | --- | ---: |
| Gluetun | NordVPN tunnel, firewall, and qBittorrent network namespace | — |
| qBittorrent | Categorized movie and series acquisition | 8080 / 18080 |
| Prowlarr | Approved Public Torrent Sources and Arr application links | 9696 / 19696 |
| Radarr | Movie Library management | 7878 / 17878 |
| Sonarr | Series Library management | 8989 / 18989 |
| Profilarr | Pinned TRaSH-derived quality, naming, and media policy | 6868 / 16868 |
| Jellyfin | Household media playback | 8096 / 18096 |
| Seerr | Household requests and approvals | 5055 / 15055 |

¹ Production / Staging examples from the proposed configuration. All bind-address/port tuples are configurable and must be
unique across environments.

The first milestone intentionally excludes Bazarr, Usenet, FlareSolverr, Recyclarr, a public reverse proxy, SSO, and a full
metrics/logging stack. Every long-running service uses `restart: unless-stopped` and bounded `json-file` logs by default.

## Configuration model

The stack separates intent, versions, and secrets because they change at different rates and need different handling:

```text
stacks/media/
├── media-stack.yaml              # reviewed operator intent for both environments
├── versions.yaml                 # pinned image digests and policy revision
├── secrets/
│   ├── production.sops.yaml      # SOPS-encrypted Production secrets
│   └── staging.sops.yaml         # SOPS-encrypted Staging secrets
├── fixtures/                     # explicit legal verification fixtures
└── README.md
```

The current `media-stack.yaml` schema contains only the fields below. Unknown fields are rejected, which prevents misspellings
from silently becoming ineffective configuration.

| YAML field | Current consumer | Exact effect |
| --- | --- | --- |
| `apiVersion` | all commands | Must be `homelab.media-stack/v1alpha1`. |
| `kind` | all commands | Must be `MediaStack`. |
| `spec.defaults.timezone` | `init`, `plan` | Validated as an IANA timezone; rendered as `TZ` for every service. |
| `spec.defaults.runtimeUID` / `runtimeGID` | `init`, `plan` | Own newly created data directories; render as `PUID`/`PGID` for LinuxServer-style services and Compose `user` for Jellyfin and Seerr. |
| `spec.defaults.lanBindAddress` | `plan` | Host address used for every published web port. `0.0.0.0` listens on all host interfaces. |
| `spec.defaults.backupRetention.daily` / `weekly` / `monthly` | `backup` | Counts of unprotected archives retained in distinct UTC day, ISO-week, and UTC-month buckets. The checked-in policy is 7/4/6; counts must be non-negative and at least one tier must retain an archive. |
| `spec.environments.<name>.projectName` | `plan`, `apply` | Compose project name; scopes networks, runtime secret paths, and config volumes independently for Production and Staging. |
| `spec.environments.<name>.dataRoot` | `init`, `plan` | Absolute host root provisioned by `init`; mounted as the selected Environment `/data` seam. The other Environment root is never rendered. |
| `spec.environments.<name>.backupRoot` | `backup` | Absolute host namespace for the Environment's application-state archives. Production and Staging roots must be non-overlapping and neither may overlap either Environment's `dataRoot`; `backup` creates the selected root privately and atomically publishes verified backup directories beneath it. |
| `spec.environments.<name>.secretsFile` | `init`, `doctor`, `apply`, Promotion verification | Environment-specific SOPS document written by `init`; `doctor` proves it can be decrypted, `apply` uses its OpenVPN, Profilarr, and Jellyfin credentials while materializing only runtime values required by containers, and Promotion verification authenticates to Jellyfin. `plan` never decrypts it. |
| `spec.environments.<name>.hardwareTranscoding` | `init`, `doctor`, `plan` | Must be `auto` or `disabled`. `auto` detects and validates `/dev/dri/renderD128`; supported hosts add the Jellyfin device and numeric render group, while missing hardware keeps the portable topology. `disabled` always keeps the portable topology. |
| `spec.environments.<name>.ports.qbittorrent` | `plan` | Host port published on Gluetun for the qBittorrent Web UI target `8080`; qBittorrent has no direct published port. |
| `spec.environments.<name>.ports.prowlarr` | `plan` | Host port for Prowlarr target `9696`. |
| `spec.environments.<name>.ports.sonarr` | `plan` | Host port for Sonarr target `8989`. |
| `spec.environments.<name>.ports.radarr` | `plan` | Host port for Radarr target `7878`. |
| `spec.environments.<name>.ports.profilarr` | `plan` | Host port for Profilarr target `6868`. |
| `spec.environments.<name>.ports.jellyfin` | `plan` | Host port for Jellyfin target `8096`. |
| `spec.environments.<name>.ports.seerr` | `plan` | Host port for Seerr target `5055`. |
| `spec.acquisition.vpn.provider` / `protocol` | `init`, `doctor`, `plan`, `apply` | Currently fixed to `nordvpn` and `openvpn`; rendered as Gluetun's explicit `VPN_SERVICE_PROVIDER` and `VPN_TYPE`. The CLI never requests a Nord access token or calls a Nord API. |
| `spec.acquisition.vpn.openvpnProtocol` | `init`, `doctor`, `plan`, `apply` | `udp` by default or explicit `tcp`; validated against the pinned catalogue and rendered as `OPENVPN_PROTOCOL`. |
| `spec.acquisition.vpn.server.countries` | `init`, `doctor`, `plan`, `apply` | Non-empty semantic country selection rendered as `SERVER_COUNTRIES`; an empty selection is rejected before Docker starts. |
| `spec.acquisition.vpn.server.categories` | `init`, `doctor`, `plan`, `apply` | Optional `P2P` category rendered as `SERVER_CATEGORIES`. |
| `spec.acquisition.vpn.catalogueUpdateInterval` | `init`, `plan`, `apply` | Must parse as at least `360h`; rendered as `UPDATER_PERIOD`, with `/gluetun` backed by the Environment's persistent named volume. |
| `spec.acquisition.publicTorrentSources[].id` / `enabled` | `apply` | Selects a semantic source from the CLI's approved catalog. The current catalog contains only `internetarchive`, enabled in the checked-in configuration. Unknown IDs are rejected. |

The separate `LegalMovieFixture` document is a versioned verification input, not Declared Configuration. Its `title` and
positive `tmdbId` identify the public-domain movie in Radarr; `releaseTitle` and the fixed approved `Internet Archive`
`indexer` select one exact discovered result; and `timeout` bounds acquisition plus import. Unknown fixture fields and other
indexers are rejected.

The `LegalSeriesFixture` is likewise a versioned verification input. Its `title` and positive `tvdbId` identify the
public-domain series in Sonarr; `seasonNumber` and `episodeNumber` select one episode; `releaseTitle` and the approved
`Internet Archive` indexer select one exact release; and `timeout` bounds acquisition plus import. Unknown fields, invalid
episode coordinates, and other indexers are rejected.

Container paths, network names, config-volume names, application URLs, qBittorrent categories, restart policy, and logging
limits are derived rather than exposed as arbitrary YAML. This preserves the shared `/data` and environment-isolation
invariants.

`versions.yaml` pins every container image by digest and pins the Profilarr PCD policy input. A version-only change is still a
reviewable, verifiable change and invalidates evidence created for older digests.

### Configuration ownership

| Owner | Declared responsibility |
| --- | --- |
| Docker Compose | Images, containers, networks, ports, health checks, mounts, volumes, lifecycle, and logging |
| `media-stack` CLI | Root folders, qBittorrent settings/categories, download clients, indexers, application links, Jellyfin Movie and Series Libraries, destructive-deletion policy, and general settings |
| Profilarr | Sonarr/Radarr quality profiles, custom formats, quality definitions, naming, upgrades, and media-management policy |
| Human through guided setup | Credentials and the first Profilarr connections |

Keeping one writer per configuration domain prevents reconciliation loops. In particular, Recyclarr is excluded because it
would compete with Profilarr for the same Sonarr and Radarr policy.

### Reconciliation (planned)

The target workflow keeps Declared Configuration in the repository and observed state in the applications. The completed `plan` will compare the two and report
ordered `create`, `update`, `restart`, `guide`, `verify`, `unknown`, and eligible `delete` actions. `apply` creates missing
resources and repairs drift through supported interfaces. A successful repeated apply converges to an empty plan.

Unknown resources are reported and retained by default. Pruning is opt-in and limited to CLI-owned configuration; it requires
a current backup, unchanged preview, explicit flag, and confirmation. It never prunes media, torrent payloads,
Profilarr-owned policy, secrets, or backups.

## Secrets

Secrets are committed only as SOPS-encrypted values using age recipients. Age private identities remain outside Git. The CLI
decrypts narrowly at runtime and uses Compose secret files where the target image supports them; plaintext values must not
appear in arguments, rendered Compose, plans, reports, logs, diagnostics, or backup manifests.

`apply` writes `openvpn_user`, `openvpn_password`, and `profilarr.env` beneath
`$XDG_RUNTIME_DIR/media-stack/<projectName>/` (or a per-user directory under the system temporary directory when
`XDG_RUNTIME_DIR` is unavailable). The directory is mode `0700` and every file is mode `0600`; only Gluetun receives the
OpenVPN Compose secret mounts, while Profilarr receives its API key through the owner-only runtime environment file. Values are intentionally not stored in plaintext in the repository or embedded in rendered Compose. They
remain present for container restart support and are atomically replaced on the next `apply`; ending the user's runtime
session removes the standard runtime directory.

Each Environment's SOPS document must also contain a unique Profilarr API key of at least 32 characters and distinct Jellyfin administrator credentials:

```yaml
profilarr:
  apiKey: <unique-high-entropy-value>
jellyfin:
  username: <environment-specific-administrator>
  password: <unique-high-entropy-password>
```

New `init` runs collect and encrypt these values. For an Environment initialized before Profilarr or Jellyfin reconciliation was implemented,
open its existing document with `sops stacks/media/secrets/<environment>.sops.yaml`, add the block above, save it, and rerun
`apply`. The Profilarr key declaratively configures Profilarr and authenticates only its documented versioned API. The Jellyfin credentials bootstrap or authenticate one local administrator through Jellyfin's supported API. These values remain in memory, are never placed in Compose, and are never printed.

The initial VPN path uses NordVPN OpenVPN manual-setup service username/password with UDP by default and TCP as an explicit
fallback. Gluetun selects endpoints from semantic country and optional category filters, persists its server catalogue, and
may refresh it no more often than the configured minimum interval. The CLI does not request a Nord access token, call a Nord
API, or pin a single server hostname. WireGuard/NordLynx remains deferred until supported private-key acquisition is proven.

Generated application API keys stay in environment-specific mutable application state and are covered by backups unless a
documented supported interface allows stable creation and storage.

## Applying configuration

The available phase validates and renders the selected Environment, materializes runtime secrets, starts Gluetun,
waits boundedly for health, starts and reconciles qBittorrent in Gluetun's network namespace, then starts Radarr and Sonarr
and reconciles their Movie Library and Series Library contracts through supported APIs. It then prepares Prowlarr discovery,
starts Profilarr, and verifies its connections through `GET /api/v1/arr/instances`. It then starts Jellyfin, completes first-run setup when needed, authenticates, creates the Movie Library at `/data/media/movies` and Series Library at `/data/media/series`, and disables both global and per-library destructive deletion while preserving unrelated libraries and user policy. Repeated apply makes no Jellyfin writes after convergence.

On the first run, `apply` reports `manual action required` until the operator opens the selected Environment's Profilarr UI
and adds exactly these enabled connections:

- Radarr at `http://radarr:7878`, using Radarr's API key from **Settings > General**.
- Sonarr at `http://sonarr:8989`, using Sonarr's API key from **Settings > General**.

Save and test both connections in Profilarr, then rerun the same `media-stack apply` command. The rerun verifies their types,
enabled state, and internal service URLs through Profilarr's documented API and continues without relying on private UI
routes or direct database access. Perform this checklist independently in Production and Staging because their application
state and credentials are isolated.

The complete apply order will be:

```mermaid
flowchart TD
    Validate[Validate host, configuration, and secrets] --> Render[Render and validate Compose]
    Render --> Storage[Provision isolated directories and volumes]
    Storage --> Start[Start Gluetun and applications]
    Start --> Ready[Wait for health and supported APIs]
    Ready --> QB[Reconcile qBittorrent]
    QB --> Arr[Reconcile Radarr, Sonarr, and Prowlarr]
    Arr --> Profile[Guide and verify Profilarr connections]
    Profile --> Policy[Allow Profilarr to apply policy]
    Policy --> Frontend[Reconcile Jellyfin and Seerr]
    Frontend --> Converge[Verify convergence]
```

If the operation is interrupted, the CLI reports completed, failed, and pending actions. It does not claim rollback where an
application cannot provide it; a later run observes current state and safely resumes toward the declaration.

## Verification suites

| Suite | Intended evidence |
| --- | --- |
| `smoke` | Rendered schema, isolation, health, API readiness, LAN reachability, and service-name resolution |
| `full` | Smoke plus storage semantics, convergence, backup sampling, VPN identity, fail-closed behavior, and recovery |
| `promotion` | Full plus legal discovery, acquisition, hardlink import, authenticated Jellyfin discovery, and direct-play readiness |
| `restore-drill` | Recovered state, isolation, rotated credentials, disabled acquisition, and gated integrations |

Disruptive checks are confined to disposable resources or Staging and restore any perturbed state. Run `MEDIA_STACK_LIVE_JELLYFIN=1 go test ./tests/acceptance -run TestDisposableJellyfinServesImportedMovieReadOnly` to launch the pinned image with temporary state and prove authenticated discovery, direct-play readiness, and read-only media access against a real Jellyfin API. Production permits only
non-disruptive smoke observations. The detailed testing strategy is being resolved in the
[testing Wayfinder map](https://github.com/adkulas/homelab/issues/37).

## Backups, restore, and Restore Drills

```bash
media-stack backup --environment production --protect --label before-upgrade
media-stack restore --environment production --backup <backup-id>
media-stack restore \
  --environment staging \
  --backup <production-backup-id> \
  --as-restore-drill \
  --credentials stacks/media/secrets/staging-drill.sops.yaml
media-stack verify --environment staging --suite restore-drill
```

A backup quiesces services when required, archives every mutable service configuration volume, calculates checksums, and
writes a versioned manifest last. Media and incomplete downloads are deliberately excluded: repository configuration and
application-state backups reconstruct the stack, while media protection is a separate storage policy.

The command writes `<backupRoot>/<backup-id>/manifest.json` and one tar archive per rendered named volume under
`archives/`. The manifest records Environment and Compose-project identity, the Declared Configuration and checked-in
version digests, immutable service images, Docker volume and mount identities, archive paths, sizes, SHA-256 checksums, and
the consistency method. A failed run remains under `.incomplete-<backup-id>` without a manifest and cannot be restored.

After publishing and verifying a backup, the command applies the configured retention policy to the selected Environment
published archives. The checked-in policy keeps 7 daily, 4 weekly, and 6 monthly archives. A retained archive is the newest
unprotected backup in each distinct UTC day, ISO week, or UTC month bucket, selected in that order. Protected backups are
never expired by retention. Production and Staging backups use separate namespaces; the backup root should preferably live
on another disk or NAS.

Ordinary restore only accepts a backup from the same environment. A Restore Drill is the one Production-to-Staging
exception: it preserves Staging identity, rotates external credentials, starts with acquisition disabled, and gates
integrations until confirmed. Production Profilarr state embeds Production-specific connections, so the initial design keeps
Staging's Profilarr state and records Production Profilarr as an explicit drill exclusion.

## Command reference

| Command | Availability | What it does and why to use it |
| --- | --- | --- |
| `media-stack init --environment production|staging [--config path]` | Available | Interactively completes runtime/VPN choices, collects a unique Profilarr API key and distinct Jellyfin administrator credentials, encrypts that Environment's credentials, and provisions its isolated data layout. Use it once per Environment and rerun it to restore missing directories without replacing complete choices. |
| `media-stack init --environment ... --non-interactive --answers path` | Available | Performs the same initialization from a strict answer document. Use it for repeatable automation; missing or unknown answers fail. |
| `media-stack doctor --environment production|staging [--config path] [--output human|json]` | Available | Runs host, tooling, secret, TUN, Gluetun-filter, and container-visible storage preflights through the declared runtime identity. Use it before attempting startup; exit `1` means at least one diagnostic failed. |
| `media-stack plan --environment production|staging [--config path]` | Available, render-only | Writes rendered Compose YAML to stdout without mutation. Use it to inspect the selected Environment topology or pipe it to `docker compose config`. Application observation and saved Plan Artifacts are not implemented yet. |
| `media-stack apply --environment production|staging [--config path]` | Available through Jellyfin Movie and Series Library reconciliation | Uses the selected Environment's OpenVPN, Profilarr, and Jellyfin credentials without printing them; starts and reconciles qBittorrent, Radarr, Sonarr, Prowlarr, and Profilarr; then starts Jellyfin, bootstraps or authenticates its administrator, creates both libraries, and disables destructive deletion. An incomplete Profilarr connection still prints the exact UI checklist and exits `1`. |
| `media-stack backup --environment production|staging [--config path] [--label text] [--protect] [--output human|json]` | Available | Stops only running services with mutable named volumes, archives each volume through a read-only Docker mount, resumes those services, independently verifies every checksum, atomically publishes the selected Environment manifest last, then expires unprotected archives according to `backupRetention`. Use `--protect` for an archive that retention must preserve. |
| `media-stack restore` | Planned | Will preview and safely restore compatible Environment state. It is not accepted by the current binary. |
| `media-stack verify --environment staging --suite full [--config path] [--output human|json]` | Available, VPN verification phase | Proves TUN availability, healthy tunneled qBittorrent egress distinct from host egress, fail-closed behavior during a controlled Gluetun stop, and recovery. Use it after `apply` in Staging; it is deliberately rejected for Production. |
| `media-stack verify --environment staging --suite promotion --legal-fixture path [--config path] [--output human|json]` | Available through Movie Library playback proof | Runs the full VPN proof, registers the fixture movie in Radarr, grabs the exact Internet Archive release into qBittorrent, proves source/imported inode identity, authenticates to Jellyfin, discovers the exact imported path, and requires direct-play readiness. Use only in a disposable Staging Environment. Verification Artifact behavior remains planned. |
| `media-stack verify --environment staging --suite promotion --legal-series-fixture path [--config path] [--output human|json]` | Available through Series Library playback proof | Runs the full VPN proof, registers the fixture series and episode in Sonarr, grabs the exact Internet Archive release into qBittorrent, waits for Sonarr import, proves source/imported inode identity, authenticates to Jellyfin, discovers the exact imported episode, and requires direct-play readiness. Supply both legal fixture flags to prove both libraries in one run. Use only in a disposable Staging Environment. |
| `media-stack destroy` | Planned | Will remove only selected-Environment runtime resources behind explicit safety guards. It is not accepted by the current binary. |

Every currently available command requires an explicit `production` or `staging`; Production is never inferred. A relative
`--config` path is resolved from the working directory, while omitting it selects `stacks/media/media-stack.yaml` from the
repository root.

The current qBittorrent, Radarr, Sonarr, Prowlarr, Profilarr, and Jellyfin apply phases share the renderer with `plan`; later mutation phases will add saved
Plan Artifacts, Environment locking, confirmation, and volatile safety rechecks. Planned `destroy` will never purge media, torrent payloads,
backups, secrets, or Declared Configuration.

## Operating principles

- Start in Staging and Promote exact verified changes.
- Treat `up -d` as container startup, not proof that the system works.
- Use application APIs and CLI evidence instead of relying on remembered UI state.
- Pin images and policy inputs; never deploy mutable `latest` tags.
- Keep active downloads and libraries on the same capable filesystem.
- Let Sonarr and Radarr own deliberate library removal; Jellyfin and Seerr do not delete media.
- Back up before pruning, replacing mutable state, changing images, restoring, or removing volumes.
- Never weaken Gluetun's firewall to make acquisition work.

## Design references

- [Media Stack implementation plan](../../docs/plans/media-stack.md)
- [Media Stack CLI and Declared Configuration design](../../docs/plans/media-stack-cli.md)
- [ADR: cross-platform Compose Media Stack](../../docs/adr/0001-cross-platform-compose-media-stack.md)
- [ADR: Profilarr owns TRaSH-derived policy](../../docs/adr/0002-profilarr-owns-trash-derived-policy.md)
- [ADR: isolate Production and Staging](../../docs/adr/0003-isolate-production-and-staging.md)
- [Gluetun and NordVPN research](../../docs/research/gluetun-nordvpn.md)
- [arr-new teaching-reference analysis](../../docs/research/arr-new-compose.md)
- [Home Media Stack implementation map](https://github.com/adkulas/homelab/issues/36)
- [Media Stack testing-strategy map](https://github.com/adkulas/homelab/issues/37)
