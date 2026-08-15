# Media Stack CLI and Declared Configuration design

Status: proposed

This document refines the operator interface described in the [Media Stack implementation plan](media-stack.md). Existing ADRs
remain authoritative.

## Design summary

Keep the eight promised commands:

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

Promotion, pruning, and Restore Drills are guarded modes rather than additional top-level commands:

- Promotion is a Production `plan` and `apply` constrained by an exact Staging Verification Artifact.
- Pruning is `plan --prune` followed by `apply --plan <file> --prune`.
- A Restore Drill is `restore --as-restore-drill` followed by `verify --suite restore-drill`.

The CLI process is the public seam: arguments, prompts, exit status, human or JSON output, artifacts, and changes observable
through Docker Compose and supported application interfaces.

Internally, keep orchestration behind a small interface:

```go
type Engine interface {
    Plan(context.Context, Request) (Plan, error)
    Execute(context.Context, Plan) (Report, error)
}
```

`Request` is a closed tagged union, not a bag of optional fields. `Plan` can be created only through validated constructors.
The implementation hides configuration compilation, Compose rendering, ordering, secret resolution and redaction, service
reconciliation, guided actions, backup consistency, recovery, and evidence validation.

Do not introduce a generic stack-driver or plugin interface yet. With only the Media Stack, that would be a hypothetical seam.
Keep code placed so a second real stack can later reveal the interface worth extracting.

## Global command contract

### Selection and output

- `--config` defaults to `stacks/media/media-stack.yaml` relative to the repository root.
- Every environment-scoped invocation selects exactly one Production or Staging Environment.
- Production is never the default. A Production mutation must explicitly name it, directly or in a validated artifact.
- `--output human|json` defaults to `human`. JSON is one versioned document on stdout; progress goes to stderr.
- Unknown fields and unsupported schema versions are errors.

### Plans and execution

- Every mutation uses the same planner as `media-stack plan` and displays actions before execution.
- A saved Plan Artifact binds operation, environment identity, canonical Declared Configuration digest, checked-in version
  digest, observed-state digest, options, expiry, ordered actions, prerequisites, and evidence.
- Execution rejects a changed, expired, or tampered artifact instead of silently recalculating a different plan.
- One mutation may hold an environment lock. Apply, backup, restore, and destroy cannot race.
- Failures report completed, failed, and pending actions. The CLI does not promise rollback where an application cannot.
- Secrets never appear in argv, plans, reports, logs, diagnostics, or backup manifests.

### Safety

- Unknown resources are reported and retained by default.
- Pruning is limited to CLI-owned configuration. It never includes media, torrent payloads, Profilarr-owned policy, secrets,
  or backups.
- Pruning and replacement of mutable state require a current backup, unchanged preview, explicit flag, and confirmation.
- `--yes` is allowed only with a saved Plan Artifact in non-interactive operation.
- Production image changes and destructive migrations require exact, unexpired Staging verification and a current Production
  backup.
- Cleanup always attempts to resume services quiesced by backup or verification.

### Results

Diagnostics have a stable code, severity, environment, subject, explanation, remedy, and retryability. Code families are
`USAGE_*`, `CONFIG_*`, `DEPENDENCY_*`, `SECRET_*`, `PREFLIGHT_*`, `OBSERVE_*`, `PLAN_*`, `APPLY_*`,
`SAFETY_*`, `BACKUP_*`, `RESTORE_*`, and `VERIFY_*`.

```text
0   completed; a plan may contain changes
1   operational or verification failure
2   plan contains changes, only with --detailed-exitcode
64  usage or configuration error
75  stale plan, held lock, or retryable conflict
```

Detailed classification belongs in structured output, not an ever-growing set of exit codes.

## Command behavior

### `init`

```text
media-stack init --environment staging
media-stack init --environment production --non-interactive --answers answers.yaml
```

`init` locates the repository, collects roots, project identity, LAN ports, timezone, numeric `runtimeUID` and `runtimeGID`,
hardware preference, age recipient, and secret values or references. It may suggest `1000` only when that value matches the
selected host user's actual numeric identity; it never silently hard-codes `1000:1000`. It validates cross-environment
isolation, previews changes, atomically writes configuration and the SOPS-encrypted secret document, and creates
selected-environment directories with safe permissions.

It never starts containers. Re-running preserves choices and prompts only for missing values. It reports invalid existing
values without overwriting them. Non-interactive use fails when an answer is missing.

### `doctor`

```text
media-stack doctor --environment staging [--output json]
```

`doctor` checks configuration, secret references, isolation, supported Ubuntu or WSL 2 execution, Docker, Compose, SOPS,
age, ports, writable roots, free space, same-device layout, hardlink inode preservation, atomic rename, permissions,
filesystem events, and optional transcoding hardware. Storage permission probes run through disposable containers using the
declared runtime identity and the same container-visible mounts as the applications; they prove create, rename, hardlink,
inode preservation, and removal rather than relying only on host-user access.

Storage checks use uniquely named disposable files under the selected data root and remove only those files. Native
Windows/NTFS storage remains unsupported unless all required container-observable probes pass; passing remains a warning.
Independent checks continue and report `pass`, `warn`, `fail`, or `skip` with remedies.

### `plan`

```text
media-stack plan --environment staging [--out plan.json] [--detailed-exitcode]
media-stack plan --environment production --verified-by staging-verification.json
media-stack plan --environment production --prune --verified-backup backup-manifest.json
```

`plan` compiles configuration, checks secret availability, renders and validates Compose, observes supported interfaces, and
emits stable ordered `create`, `update`, `restart`, `guide`, `verify`, `unknown`, and eligible `delete` actions.

It never starts the stack. If an application does not yet exist, its observation is `deferred`. Apply starts and observes it
before resolving that phase, recording resolved actions rather than pretending they were known earlier. `--prune` adds
eligible removals to the preview but grants no execution permission.

### `apply`

```text
media-stack apply --environment staging
media-stack apply --plan plan.json
media-stack apply --plan prune-plan.json --prune --yes
```

Without `--plan`, apply plans, displays, confirms, and executes. With one, it validates evidence, rechecks volatile safety
conditions, and executes exactly the plan plus explicitly identified deferred phases.

```text
validate prerequisites
→ resolve secret references
→ render and validate Compose
→ provision isolated directories and volumes
→ start Gluetun and applications
→ wait for health and supported interfaces
→ reconcile qBittorrent
→ reconcile Radarr and Sonarr
→ reconcile Prowlarr links and Public Torrent Sources
→ guide and verify the Profilarr connection
→ allow Profilarr to apply its owned policy
→ reconcile Jellyfin and Seerr
→ verify convergence
```

The Profilarr UI step creates a durable `manual_action_required` checkpoint. Re-running verifies completion through supported
observable interfaces and continues. Apply succeeds only after a final observation finds no unexpected drift.

Promotion is a Production apply whose plan carries successful Staging verification for the exact configuration digest,
version digest, and CLI contract version. Changed inputs invalidate the evidence.

### `backup`

```text
media-stack backup --environment production [--protect] [--label before-upgrade]
```

`backup` plans coverage and space, observes versions, quiesces applications as needed, archives every mutable configuration
volume, resumes services, independently calculates checksums, writes the manifest last, atomically publishes, verifies, and
only then applies retention.

Media and incomplete downloads are excluded. Production and Staging use separate namespaces. Incomplete backups are visible
but ineligible for restore. Protected or Promotion-referenced backups are never removed by retention.

The manifest records schema and CLI contract versions, environment identity, configuration digest, services, volumes, image
versions, timestamp, consistency methods, checksums, and protection status.

### `restore`

```text
media-stack restore --environment production --backup <id>
media-stack restore --environment staging --backup <production-id> --as-restore-drill \
  --credentials secrets/staging-drill.sops.yaml
media-stack restore --resume <operation-id>
```

Restore first verifies manifest schema, checksums, service coverage, source environment, and compatibility. It produces a
replacement Plan Artifact and requires a verified safety backup before execution.

Execution restores into temporary volumes, verifies contents, swaps them into place, and starts services in dependency order.
An operation journal supports resume or rollback after interruption. Media is never restored by this command.

Ordinary restore requires matching environments. A Restore Drill is the only exception and permits Production to Staging. It
preserves Staging identity, rotates external credentials, starts with acquisition disabled, and gates integrations. It never
permits Staging to Production.

Production Profilarr state embeds Production connections and cannot be a Staging template. Initially, a Restore Drill keeps
Staging's Profilarr state and reports Production Profilarr as an explicit exclusion. A sanctioned export/import or rewrite
interface is needed before that part can be drilled safely.

### `verify`

```text
media-stack verify --environment staging [--suite smoke|full|promotion|restore-drill]
media-stack verify --environment staging --suite promotion --legal-fixture fixtures/legal-movie.yaml
```

- `smoke`: rendered schema, isolation, health, interface readiness, LAN reachability, and internal service-name resolution,
  including Gluetun's `qbittorrent` alias.
- `full`: smoke plus storage semantics through the declared application runtime identity, convergence, backup sampling, VPN
  egress, and fail-closed behavior.
- `promotion`: full plus required legal acquisition, hardlink import, discovery, and playback checks.
- `restore-drill`: recovered state, isolation, rotated credentials, disabled acquisition, and gated integrations.

Disruptive checks restore perturbed state. Legal acquisition requires an explicit repository fixture. Success emits a
content-addressed Verification Artifact binding environment, configuration and version digests, apply result, suites,
results, timestamp, and expiry. Failure cannot be downgraded to enable Promotion.

### `destroy`

```text
media-stack destroy --environment staging
media-stack destroy --environment production --verified-backup backup-manifest.json
```

`destroy` previews selected-environment containers, networks, generated runtime files, and mutable volumes. Execution needs
a saved plan, confirmation, and an identity recheck. Production confirmation includes typing its environment name.

By default it removes only containers and networks. Volumes require `--volumes`, a current backup, and a second confirmation.
It never deletes media, torrent payloads, backups, secrets, or Declared Configuration. There is no `--purge-data`.

## Declared Configuration

Use three documents with distinct lifecycles:

1. `media-stack.yaml`: reviewable operator intent for both environments.
2. `versions.yaml`: pinned images and policy inputs.
3. One SOPS document per environment: secret values.

Do not expose arbitrary Compose topology. Container paths, network and volume names, categories, application URLs, root
folders, and required service membership are derived from stable semantic identities. This makes isolation and hardlinks
enforceable.

### `media-stack.yaml`

```yaml
apiVersion: homelab.media-stack/v1alpha1
kind: MediaStack

metadata:
  name: household-media

spec:
  defaults:
    timezone: America/Toronto
    runtimeUID: 1000
    runtimeGID: 1000
    lanBindAddress: 0.0.0.0

  environments:
    production:
      projectName: media-production
      dataRoot: /srv/media/production
      backupRoot: /mnt/backups/media/production
      ports:
        qbittorrent: 8080
        prowlarr: 9696
        sonarr: 8989
        radarr: 7878
        profilarr: 6868
        jellyfin: 8096
        seerr: 5055
      secretsFile: secrets/production.sops.yaml
      hardwareTranscoding: auto
      requirePromotionEvidence: true

    staging:
      projectName: media-staging
      dataRoot: /srv/media/staging
      backupRoot: /mnt/backups/media/staging
      ports:
        qbittorrent: 18080
        prowlarr: 19696
        sonarr: 18989
        radarr: 17878
        profilarr: 16868
        jellyfin: 18096
        seerr: 15055
      secretsFile: secrets/staging.sops.yaml
      hardwareTranscoding: auto

  acquisition:
    vpn:
      provider: nordvpn
      protocol: openvpn
      openvpnProtocol: udp
      server:
        countries:
          - Canada
        categories:
          - P2P
      catalogueUpdateInterval: 480h
    publicTorrentSources:
      - id: approved-source
        enabled: true
        secretRef: prowlarr.sources.approved-source
    seeding:
      ratioLimit: 1.0
      timeLimit: 168h

  requests:
    requireApproval: true
    householdAuthentication: jellyfin
    emergencyLocalAdministrator: true

  backup:
    retention:
      daily: 7
      weekly: 4
      monthly: 6

  verification:
    legalFixtures:
      movie: fixtures/legal-movie.yaml
      series: fixtures/legal-series.yaml
```

Deliberately derive instead of configure:

- `/data/torrents/movies`, `/data/torrents/series`, `/data/media/movies`, and `/data/media/series`;
- qBittorrent categories and Automatic Torrent Management;
- required storage capabilities;
- environment-scoped network and volume names, with no fixed `container_name` values;
- Gluetun's `qbittorrent` network alias and qBittorrent Web UI publication, with peer services using
  `http://qbittorrent:8080` and no independent qBittorrent network attachment;
- Arr root folders and internal service URLs such as `http://prowlarr:9696`, `http://radarr:7878`, and
  `http://sonarr:8989`;
- runtime identity settings for storage-owning application images, using supported `PUID`/`PGID` variables or Compose `user`
  as appropriate;
- `restart: unless-stopped` and per-service `json-file` logging bounded by `max-size: 10m` and `max-file: 3`;
- mandatory first-milestone services; and
- Profilarr's owned quality, naming, upgrade, and media-management domains.

If one must vary later, add a semantic field for the use case rather than exposing an underlying Compose or application knob.

### `versions.yaml`

```yaml
apiVersion: homelab.media-stack/v1alpha1
kind: MediaStackVersions

images:
  gluetun: ghcr.io/qdm12/gluetun@sha256:<digest>
  qbittorrent: lscr.io/linuxserver/qbittorrent@sha256:<digest>
  prowlarr: lscr.io/linuxserver/prowlarr@sha256:<digest>
  sonarr: lscr.io/linuxserver/sonarr@sha256:<digest>
  radarr: lscr.io/linuxserver/radarr@sha256:<digest>
  profilarr: ghcr.io/dictionarry-hub/profilarr@sha256:<digest>
  jellyfin: jellyfin/jellyfin@sha256:<digest>
  seerr: ghcr.io/seerr-team/seerr@sha256:<digest>

policy:
  profilarrPcdRevision: <commit-or-content-digest>
```

Digests are authoritative. The Plan Artifact binds this document separately so a version-only Promotion is identifiable.

### Encrypted secrets

The plaintext shape before SOPS encryption is semantic, not container environment variables:

```yaml
nordvpn:
  openvpn:
    serviceUsername: <secret>
    servicePassword: <secret>
bootstrap:
  jellyfinAdminPassword: <secret>
  seerrEmergencyAdminPassword: <secret>
prowlarr:
  sources:
    approved-source:
      credentials: <secret>
```

Generated API keys remain mutable application state covered by backups unless a documented supported interface permits stable
creation and storage. The CLI does not scrape config files or databases to populate this document.

NordVPN account email/password and access tokens are not part of this schema. The first milestone uses manual-setup OpenVPN
service credentials. Gluetun owns endpoint selection and catalogue refresh; the CLI does not query Nord's API. WireGuard is
deferred until a supported way to acquire its required private key is proven.

### Validation

- Both environments must exist before Promotion.
- Project names, bind-address/port tuples, data roots, backup namespaces, and secret files are distinct.
- Environment roots cannot be equal, nested, or escape through normalized `..`.
- Backup roots are not inside data roots; warn when they appear to share physical storage.
- Host roots become absolute after supported local-path expansion. Arbitrary environment interpolation is forbidden.
- `runtimeUID` and `runtimeGID` are non-root numeric IDs. Rendering must map them to each pinned storage-owning application
  image's supported `PUID`/`PGID` inputs or Compose `user`; an applicable image that cannot honor the declared identity is a
  configuration error.
- Public Torrent Sources use semantic IDs from a checked-in approved catalog.
- The first milestone accepts OpenVPN UDP or TCP; UDP is the default. Server filters must match the pinned Gluetun image's
  `format-servers -nordvpn` output.
- A configured Gluetun catalogue update interval is at least 360h.
- Durations use Go syntax; ratios and retention counts are non-negative.
- Unknown fields are errors. Schema migration is explicit and previewed.
- Defaulted and derived values participate in canonical configuration digests.
- A default change changes the CLI contract version and invalidates old verification evidence.

## Internal module shape

```text
cmd/media-stack       command parsing and rendering
internal/engine       planning, execution, common safety policy
internal/config       load, default, validate, canonicalize, migrate
internal/plan         ordered actions, digests, staleness
internal/artifact     plans, verification, manifests, receipts
internal/secrets      SOPS/age resolution and redaction
internal/topology     Compose model and environment identity
internal/reconcile    current-state normalization and convergence
internal/backup       archive, retention, restore, journal
internal/verify       named assertions and evidence
```

Create adapters only where behavior varies: process execution for Docker Compose/SOPS/age, live versus fixture HTTP
transports, Ubuntu versus WSL host behavior, and filesystem/clock/prompt/journal adapters for deterministic tests.

Do not publish `StartGluetun`, `ConfigureRadarr`, or one interface per application. Those shallow interfaces leak ordering.
Test through the CLI and supported observable state; use internal adapters only for difficult failure injection.

## Open design questions

1. **Profilarr in Restore Drills:** safe exclusion means the drill is not literally every Production volume.
2. **Verification expiry:** choose wall-clock lifetime in addition to digest invalidation.
3. **Current backup:** define maximum age separately for prune, restore, image changes, and destroy.
4. **Emergency Production changes:** there is deliberately no break-glass bypass yet.
5. **Public Torrent Source catalog:** select the initial source and define its non-secret semantic fields.
