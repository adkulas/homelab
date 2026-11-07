# Bootstrap fresh Profilarr state through a pinned compatibility adapter

Accepted after the issue #84 design interview, 2026-11-03. This supersedes the guided-first-install restriction in ADR 0002
and the media-stack plan. Profilarr still exclusively owns synchronized policy. Profilarr v2.3.1 lacks supported Arr creation,
profile-selection, and Arr synchronization endpoints, so `media-stack apply` may initialize its private state exactly once.
Remove this exception when supported upstream interfaces cover that initialization.

Compatibility requires the reviewed image digest, complete migration identities, and actual migrated schema fingerprint.
Profilarr creates and migrates its database itself. Require API readiness, stop the container and prove quiescence, inspect
freshness, then back up complete Environment-specific `/config` state (including SQLite WAL/SHM) before mutation. Require
no Arr connections or associated configuration/history and no customized policy source or local policy operations. Only the
recognized untouched upstream auto-linked Dictionarry source may be replaced. Partial, ambiguous, or unsupported state fails
closed with stable diagnostics and the guided remedy. Complete existing connections bypass all private-state operations.

The adapter prepares the exact pinned trash-pcd checkout with automatic source updates disabled, then initializes two enabled
connections, all linked-source priority rows, selected policy, and immediate-first-run/daily synchronization in one immediate
SQLite transaction with foreign keys enabled. Secrets come from in-memory Arr discovery via stdin and bound parameters;
never command arguments, diagnostic text, Compose, or persistent temporary payloads. Profilarr rebuilds its own caches and
performs synchronization after restart. Verify connections through `GET /api/v1/arr` and policy through supported Arr APIs
before continuing `apply`. Raw private schema knowledge belongs only to the compatibility adapter.

Failure before successful policy verification restores the complete pre-mutation Profilarr backup. Stop and prove quiescence
before restoring; SIGINT/SIGTERM cancel the operation and run recovery with an independent deadline. A hard process/Host
failure retains the journal for supervised recovery. If recovery cannot be proven, leave Profilarr stopped and retain a recoverable backup with explicit
diagnostics. Restoration does not reverse policy changes already sent to Arr: report partial synchronization, do not delete
Arr state, and stop downstream application. Backups and credentials never cross Environment boundaries.

Initialize HD Bluray + WEB as the ordinary movie profile, Remux 2160p (Alternative) as an opt-in movie profile, and WEB-1080p
for series. Use upstream size limits with no additional remux cap, original audio, Jellyfin naming, Default media settings,
and the PCD Default zero-delay profile. Anime is deferred. Synchronize immediately and daily from the pinned revision;
upstream revisions require review. Existing managed installations retain Profilarr ownership and receive no database repair.

Seerr keeps one default non-4K Radarr destination. Grant Advanced Requests to existing household requesters while preserving
their other permissions, and include it in new-user defaults, allowing a profile choice for new requests. This permission also
exposes available folder/server/tag controls. Upgrading an already downloaded movie remains an administrator operation in
Radarr; this does not maintain two copies. Separate 4K destinations and anime routing are outside this work.

Acceptance evidence is the public `media-stack apply` boundary, including a real pinned container with disposable state,
observed Arr synchronization, repeat application without database mutation, restored/partial-state preservation, compatibility
rejection, transaction/restart/verification recovery, secret redaction, and Environment isolation. Seerr behavior is tested at
its supported API. Narrow database tests may exercise compatibility and failure injection. These seams were confirmed in the
interview; decisions and acceptance rows are tracked in the implementation notes for issue #84.
