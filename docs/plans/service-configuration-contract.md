# Service Configuration Contract

## Status

Implemented by `media-stack config describe`.

## Problem

The Media Stack currently spreads its configuration interface across `media-stack.yaml`, `versions.yaml`, encrypted secret
documents, policy fixtures, derived topology, and reconciliation code. An operator can inspect the main YAML example but
cannot ask the CLI which settings it controls for a service, whether a setting is editable, or how a change reaches the
service.

Sonarr file naming demonstrates the ambiguity. The repository records and verifies the naming policy in
`stacks/media/fixtures/profilarr-series-policy.yaml`, but it is not an operator field in `media-stack.yaml` and the CLI does
not apply it directly. Profilarr synchronizes the pinned policy and `media-stack apply` verifies the resulting Sonarr state.
A user-visible contract must make that distinction explicit.

## Decision

The CLI exposes one complete **Service Configuration Contract** for every required service:

```text
media-stack config describe [--service <name>] [--output human|json]
```

The command describes the CLI's configuration interface; it does not inspect a running Environment or print current values.
Without `--service`, it returns every required service in stable service-name and setting-name order. The supported service
names are `gluetun`, `qbittorrent`, `prowlarr`, `sonarr`, `radarr`, `profilarr`, `jellyfin`, and `seerr`.

The contract is exhaustive for settings the CLI declares, derives, fixes, sources from secrets, synchronizes through an
external policy owner, observes, or reconciles. It does not attempt to enumerate every upstream application option. Each
service explicitly states that upstream settings absent from the contract are **Unmanaged Configuration** and are neither
promised nor repaired by the CLI.

## Setting control classes

Every setting has exactly one control class:

- `declared`: an operator may set the value through a documented field in `media-stack.yaml` or `versions.yaml`;
- `secret`: an operator supplies the value through the selected Environment's SOPS document, and output never includes the
  value;
- `derived`: the CLI computes the value from Declared Configuration and stable semantic identities;
- `fixed`: the value is Stack Policy and has no operator override;
- `externally-synchronized`: a named external policy owner applies the value and the CLI verifies it through a supported
  interface; and
- `unmanaged`: the upstream application owns the value and the CLI does not promise to observe, apply, or repair it.

`declared` means supported operator choice. Editing implementation constants or policy fixtures does not turn a setting into
a declared setting.

## Per-setting contract

Each contract entry contains:

- stable setting identifier and human name;
- service name;
- control class;
- semantic description;
- declaration path or authoritative source when one exists;
- type, allowed values, and default when applicable;
- whether the setting is sensitive;
- lifecycle: render, initialize, reconcile, externally synchronize, verify, or preserve;
- implementation status: `implemented` or `planned`; and
- a concise explanation of how an operator changes it, or why it cannot be changed.

Paths use semantic document paths such as `spec.environments.<environment>.ports.sonarr`, not Go fields, environment
variables, container internals, or application database columns.

## Required service coverage

The first contract must cover at least the following existing behavior. Implementation work must inspect the renderers and
reconcilers and add any setting they read, write, or verify that is missing here.

| Service | Required subjects |
| --- | --- |
| Gluetun | image, VPN provider and protocol, OpenVPN transport, server filters, catalogue interval, service credentials, TUN and capability contract, firewall inputs, network aliases, and qBittorrent port publication |
| qBittorrent | image, canonical Environment port, runtime identity, mounts, VPN-shared networking, current-start temporary bootstrap, stable SOPS-backed Web UI credentials, protected API verification, restart and peer authentication, save path, Automatic Torrent Management, movie and series categories, seeding limits, and unmanaged preferences |
| Prowlarr | image, LAN port, runtime identity, approved Public Torrent Sources, source details, Radarr and Sonarr links, synchronization mode, internal URLs, API keys, and unmanaged indexers/applications |
| Sonarr | image, LAN port, runtime identity, Series Library root, qBittorrent client and category, internal URLs and credentials, quality profile, quality definitions, custom-format scores, naming formats, media management, and unmanaged settings |
| Radarr | image, LAN port, runtime identity, Movie Library root, qBittorrent client and category, internal URLs and credentials, quality profile, quality definitions, custom-format scores, naming formats, media management, and unmanaged settings |
| Profilarr | image, LAN port, API key, pinned PCD repository revision, guided Radarr and Sonarr connections, owned policy domains, and unmanaged settings |
| Jellyfin | image, LAN port, runtime identity, hardware-transcoding preference and derived device mapping, administrator credentials, startup locale and remote-access policy, Movie and Series Library definitions, deletion policy, and unmanaged settings |
| Seerr | image, LAN port, runtime identity, Jellyfin and emergency-local authentication, default request permission, initialization state, internal Jellyfin connection, and unmanaged settings |

Cross-service defaults such as timezone, data and backup roots, project identity, restart policy, logging limits, volumes,
and network names appear under every affected service rather than only in an undocumented global section. A separate global
summary may remove repetition in human output, but filtered service output must remain self-contained.

## Sonarr naming example

Human output for Sonarr must answer the original operator question directly:

```text
Sonarr
  naming.standardEpisodeFormat
    control: externally-synchronized
    source: stacks/media/fixtures/profilarr-series-policy.yaml#naming.standardEpisodeFormat
    owner: Profilarr pinned policy
    lifecycle: synchronize, verify
    operator change: not configurable through media-stack.yaml; propose a semantic Declared Configuration field before varying
```

The other Sonarr naming fields receive equivalent entries. The command must not label them `declared` merely because their
expected values are checked into the repository.

## Human output

Human output is grouped by service and control class. It leads with declared settings, then secrets, externally synchronized
settings, derived settings, fixed settings, and the unmanaged statement. Each entry includes its path/source and change
guidance. `--service sonarr` is sufficient to answer whether any supported Sonarr setting is operator-configurable.

Unknown service names are usage errors and print the supported names. The command needs no Environment selection because it
describes the versioned contract rather than resolved values.

## JSON output

JSON is a single versioned document on stdout:

```json
{
  "schemaVersion": "homelab.media-stack/configuration-contract/v1alpha1",
  "services": [
    {
      "name": "sonarr",
      "settings": [
        {
          "id": "naming.standardEpisodeFormat",
          "control": "externally-synchronized",
          "source": "stacks/media/fixtures/profilarr-series-policy.yaml#naming.standardEpisodeFormat",
          "owner": "Profilarr pinned policy",
          "lifecycle": ["synchronize", "verify"],
          "sensitive": false,
          "status": "implemented",
          "operatorChange": "Not configurable through media-stack.yaml."
        }
      ],
      "unmanaged": "Upstream settings absent from this contract are not observed, applied, or repaired by media-stack."
    }
  ]
}
```

The JSON contract is deterministic and contains no resolved secret values. Adding a setting is backward-compatible; removing
or renaming a setting identifier or control class requires a schema-version change.

## Source of truth and drift prevention

The implementation must keep contract metadata beside the configuration or reconciliation behavior it describes, then
compose it behind one read-only interface. A second hand-maintained catalog detached from those modules is not acceptable.
Tests fail when a required service has no contract, setting identifiers are duplicated, a secret is not marked sensitive, or
human and JSON rendering omit entries.

CLI acceptance tests prove:

- all eight required services appear without a filter;
- every service can be filtered independently;
- Sonarr and Radarr naming are reported as externally synchronized rather than declared;
- every supported YAML and versions field is represented by at least one affected service;
- representative fixed and derived reconciler behavior is represented;
- unknown services fail with a stable usage error;
- JSON is deterministic, schema-versioned, and contains no secret values; and
- the command performs no Docker, network, decryption, filesystem mutation, or application calls.

## Documentation rule

Any change that adds, removes, reclassifies, or changes the lifecycle of a service setting must update its Service
Configuration Contract entry and `stacks/media/README.md` in the same change. A service-setting change is incomplete until
the human and JSON discovery surfaces describe it.

## Delivery dependency

This contract is a prerequisite for generic drift reporting and repair. Drift cannot be classified safely until operators
and the planner share the same explicit ownership inventory. Work that changes the service configuration interface must also
depend on the contract implementation or include it in scope.
