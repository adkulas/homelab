# Media Stack implementation plan

## Objective

Build a portable, idempotently configured home Media Stack for movies and episodic television. It must run on Ubuntu or on
Windows through Docker Desktop with WSL 2, follow TRaSH-derived media-management guidance, and support isolated Production and
Staging Environments. Every required service must publish a complete operator-facing
[Service Configuration Contract](service-configuration-contract.md) that distinguishes operator choices from secrets,
derived configuration, Stack Policy, Externally Synchronized Policy, and Unmanaged Configuration.

Implementation does not begin as part of this plan.

## Repository shape

The repository will contain independently deployable domain stacks. The Media Stack is the first:

```text
stacks/
  media/
    compose.yaml
    compose.staging.yaml
    config/
    secrets/
    scripts/
    tests/
    README.md
cmd/
  media-stack/
internal/
```

The existing NixOS files are outside the target architecture and may be removed during implementation. Core services remain in
the base Compose project. Environment identity and hardware acceleration use Compose overlays; profiles are reserved for
genuinely optional tools.

## First milestone

Include:

- Gluetun using NordVPN
- qBittorrent
- Prowlarr
- Sonarr
- Radarr
- Profilarr
- Jellyfin
- Seerr

Exclude Bazarr, Usenet, FlareSolverr, Recyclarr, a public reverse proxy, SSO, and full metrics or log aggregation.

Every UI is available on the LAN with authentication. Nothing is intentionally exposed to the internet.

Every long-running service uses `restart: unless-stopped` and a bounded per-service `json-file` logging policy with
`max-size: 10m` and `max-file: 3`. These lifecycle defaults are rendered and verified rather than left to mutable daemon
defaults.

## Environment isolation

Production and Staging are complete, independent Compose projects. Each has its own Gluetun, qBittorrent, application
containers, Compose project name, ports, secrets, API keys, config volumes, and data subtree.

```text
<data-root>/
  production/
    torrents/
      movies/
      series/
    media/
      movies/
      series/
  staging/
    torrents/
      movies/
      series/
    media/
      movies/
      series/
```

Each environment maps only its root to `/data`. qBittorrent receives `/data/torrents`; Sonarr and Radarr receive `/data`;
Jellyfin receives `/data/media` read-only. Staging is allowed to perform real acquisition but must never share state or
integrations with Production.

Application containers run with declared numeric runtime UID and GID values rather than assuming `1000:1000`. `doctor` proves
that the selected identity can create, rename, hardlink, and remove files through the actual container-visible mounts before
the environment starts managing data.

## Supported hosts and storage

Support Ubuntu with Docker Engine and Compose, and Windows with Docker Desktop's WSL 2 backend. Windows commands run through
Bash inside a WSL distribution integrated with Docker Desktop.

The wizard requires a deployment-specific MEDIA_DATA_ROOT. Active media and downloads should reside on a native Linux or WSL
filesystem. Native Windows/NTFS paths are experimental and remain disabled unless container-based preflight checks prove:

- downloads and libraries are on one device;
- hardlinks work and preserve inode identity;
- atomic rename works;
- required permissions work; and
- filesystem events behave adequately.

Use one named Docker volume per service for mutable configuration. Make hardware transcoding an optional, detected overlay
rather than part of the portable base stack.

## Automation architecture

A small Go CLI is the authoritative setup and reconciliation engine:

```text
media-stack init
media-stack doctor
media-stack plan
media-stack apply
media-stack backup
media-stack restore
media-stack verify
media-stack destroy
```

Every command accepts --environment production|staging and supports machine-readable output where useful. Mutation commands
produce a plan or dry run. Production changes require explicit environment selection and confirmation.

A committed setup.sh is a thin Bash launcher for Ubuntu and WSL. It locates or downloads a pinned, checksum-verified CLI
release and starts media-stack init. The Go binary contains configuration, API, reconciliation, backup, and verification
logic; shell scripts do not duplicate it.

The CLI invokes official docker compose, SOPS, and age tooling rather than reimplementing them.

The proposed command contracts, safety artifacts, and Declared Configuration schema are specified in
[`media-stack-cli.md`](media-stack-cli.md).

## Configuration ownership

 Owner                       Responsibility
━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 Docker Compose              Images, containers, networks, ports, health checks, and volumes
──────────────────────────  ──────────────────────────────────────────────────────────────────────────────────────────────────
 Go CLI                      Root folders, download clients, indexers, Prowlarr application links, qBittorrent preferences
                             and categories, and general application settings
──────────────────────────  ──────────────────────────────────────────────────────────────────────────────────────────────────
 Profilarr                   Quality profiles, custom formats, quality definitions, naming, and media-management policy
──────────────────────────  ──────────────────────────────────────────────────────────────────────────────────────────────────
 Human through the wizard    Credentials, initial user accounts, and the first Profilarr connections

Declared Configuration uses stable semantic identities. Reconciliation gets current state, creates missing resources, updates
drifted resources, and does nothing when state already matches. Unknown resources are reported by default. Pruning requires an
explicit flag, preview, confirmation, and a current backup.

## Bootstrap sequence

Apply in two phases:

1. Render and validate Compose, start containers, and wait for health and API readiness.
2. Collect supported bootstrap credentials, reconcile supported APIs, guide unavoidable UI actions, and verify convergence.

A brand-new Profilarr v2 instance cannot link Sonarr or Radarr through a documented API, environment variable, configuration
import, or CLI. Accept one guided UI step independently in each environment. Afterward, back up that environment's complete,
quiesced Profilarr /config state. Do not use undocumented endpoints or direct SQLite writes.

Environment-specific Profilarr state is not interchangeable because it embeds Arr URLs, keys, schedules, and mappings. Track
the missing API capability so the exception can be removed when Profilarr exposes a stable interface.

## Media policy

Use one Sonarr and one Radarr per environment. Profilarr applies pinned TRaSH-derived policy from a pinned PCD source:

- 1080p-focused movies;
- WEB 1080p-focused series;
- upgrades until the selected cutoff;
- no initial 4K, anime-specific, or remux-heavy profiles; and
- reviewed, documented local deviations.

This is deliberately described as TRaSH-derived: Profilarr is not currently listed as an officially supported TRaSH
synchronization tool. Recyclarr is excluded so two tools cannot write the same configuration domains.

Prowlarr begins with an explicit list of approved Public Torrent Sources. Do not install FlareSolverr by default.

qBittorrent uses Automatic Torrent Management and separate movie and series categories. Seed until ratio 1.0 or seven days,
whichever occurs first. Sonarr and Radarr import completed items through hardlinks, after which qBittorrent may remove its
torrent data without removing the library link.

Sonarr and Radarr are authoritative for deliberate library removal. Disable destructive deletion from Jellyfin and Seerr where
possible, never let reconciliation delete media, and retain removed media temporarily through an application trash or
recycling mechanism.

Seerr uses Jellyfin authentication for household users, retains an emergency local administrator, and initially requires
approval for requests.

## VPN behavior

qBittorrent shares Gluetun's network namespace and has no independent network path. Acquisition fails closed when the tunnel
is unhealthy. Verification proves qBittorrent's observed public IP differs from the host's.

Do not render fixed `container_name` values. Compose project scoping supplies environment-specific identities. Gluetun joins
the environment's application network with the `qbittorrent` network alias and publishes qBittorrent's Web UI port, so peer
services use `http://qbittorrent:8080` while qBittorrent retains no independent network attachment or published port.

NordVPN does not provide inbound port forwarding; reduced connectability and possible seeding impact are accepted initially.
Keep the provider configuration replaceable.

## Secrets

Commit secrets only as SOPS-encrypted values using age recipients. Age private identities remain outside Git. Pass decrypted
material at runtime without persisting plaintext where possible, using Compose secrets for applications that support file
credentials and tightly controlled generated environment files where they do not.

The wizard handles environment selection, data and backup roots, declared runtime UID and GID, project and port allocation,
age identity checks, secret capture, NordVPN OpenVPN service credentials, directory and volume creation, Compose launch, API
bootstrap, reconciliation, health checks, and the manual Profilarr connection step. It confirms before pruning, restoring,
replacing state, or destroying resources. Gluetun owns NordVPN endpoint discovery from semantic server filters; the CLI does
not require a Nord access token or query Nord's API.

## Backups and recovery

Git plus encrypted secrets reconstruct deployment and Declared Configuration. Versioned backups preserve mutable application
databases, qBittorrent state, users, history, and configuration not covered by APIs. Media protection is a separate storage
policy; incomplete downloads are not backed up.

Require a configurable BACKUP_ROOT, preferably on another disk or NAS. Quiesce services when required for consistent volume
archives. Each backup has a manifest containing environment, service, image version, timestamp, and checksum. Begin with
daily, weekly, and monthly retention of 7, 4, and 6 archives.

A Restore Drill restores Production backups into an isolated Staging Environment, overrides or rotates external credentials,
disables outbound acquisition initially, and reconnects integrations only after confirmation. Profilarr Production state is
restored only as Production state; it is never used as a Staging template.

## Promotion and upgrades

Changes follow this flow:

```text
edit Declared Configuration
        ↓
plan and apply to Staging
        ↓
verify, including a real legal acquisition test when appropriate
        ↓
plan Production
        ↓
explicitly promote the exact tested change
```

Pin service image versions or digests in a checked-in version file. Before an image change or destructive migration, record
the current version manifest, quiesce the affected service, back up its full config volume, test the exact version in Staging,
and only then promote it to Production.

## Verification

Automated verification covers:

- Compose rendering and schema validation;
- configuration and reconciler unit tests;
- API contract fixtures for supported service versions;
- an empty plan after a second apply;
- container health and LAN reachability;
- `/data` device, hardlink, rename, and application-runtime permission behavior through the actual container mounts;
- internal service-name resolution, including Gluetun's `qbittorrent` network alias;
- qBittorrent VPN egress and fail-closed behavior;
- a test import with verified inode equality;
- backup manifests and checksums; and
- periodic Production-backup restore into isolated Staging.

Phase one observability consists of health checks, structured CLI logs, reconciliation reports, backup reports, and optional
notification hooks. A full observability stack is deferred.

## Delivery order

1. Establish repository structure, configuration schema, pinned versions, and Compose rendering.
2. Build CLI foundations, init, doctor, and the thin setup.sh launcher.
3. Implement storage layout and filesystem capability checks.
4. Establish isolated Production and Staging topology.
5. Add Gluetun and qBittorrent with fail-closed verification.
6. Add Sonarr, Radarr, and Prowlarr reconciliation.
7. Add Profilarr, pinned TRaSH-derived policy, and its guided bootstrap exception.
8. Add Jellyfin and Seerr.
9. Complete plan/apply idempotence and integration verification.
10. Add volume backups, restore, retention, and Restore Drills.
11. Document and verify Promotion and upgrade workflows.

## Completion criteria

On a clean Ubuntu host or Windows host with Docker Desktop and WSL, an operator can run one Bash entry point and:

- create an isolated Production or Staging Environment;
- pass storage and VPN preflight checks;
- complete only the documented account and Profilarr UI steps;
- idempotently apply every supported configuration;
- request and acquire a small legal test item through Seerr;
- import it into Jellyfin through a verified hardlink;
- produce a checksummed backup;
- restore the appropriate backup into isolated Staging; and
- rerun plan with no unexpected drift.
