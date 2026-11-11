# Media Stack destroy implementation plan

Status: agreed design, confirmed by the operator on 2026-11-09; implementation pending.

This records the completed design interview. It supersedes the narrower destroy
contract in the original CLI plan, which prohibited payload and secret deletion.
It preserves ADR 0003's Environment isolation and the newer
[deployment-scoped credential ownership](scoped-media-secrets.md): application
credentials belong to one Site, Stack Instance, and Environment; NordVPN
credentials are shared within the Site.

## Operator contract

Destroy supports temporary runtime removal and a fresh Environment reset.
Permanent retirement of an Environment declaration is outside this work.

Every invocation selects exactly one Production or Staging Environment, through
`--site` plus `--instance`, or an explicit standalone `--config`. Production is
never inferred. Initially, execution is supported only on the selected Host with
its local Docker daemon. Reject remote Docker contexts and bind approval to the
actual Host and daemon identities; the inventory Host name alone is insufficient.

| Selection | Removed | Preconditions |
| --- | --- | --- |
| Default | Owned containers, networks, and generated runtime secret files | Confirmed saved plan; remove runtime files only after container removal |
| `--volumes` | Owned mutable application configuration volumes | Automatically created, protected, verified final-state backup |
| `--downloads` | Selected Environment's `torrents` subtree | Explicit payload-loss acknowledgement |
| `--media` | Selected Environment's `media` subtree | Explicit payload-loss acknowledgement |
| `--secrets` | Selected Environment's encrypted application credential document | Requires `--volumes`; preserve encrypted recovery copy first |

Containers and networks are always removed before the optional deletion actions.
Downloads, library media, and application volumes are independently selectable.
There is no ambiguous `--data` option. Preserve `dataRoot` itself.

Always retain Declared Configuration, versions, backups, shared VPN documents,
age identities, other Environments, other Stack Instances, and Site infrastructure
such as DNS, Traefik, and the CA. Retain unknown resources under the existing
ownership policy. Deleting secrets does not revoke provider credentials or erase
copies from Git history and backups.

## Preview and authorization

Use two invocations: save/display a destruction plan, then explicitly confirm and
execute that exact plan. Exact artifact and acknowledgement flag spellings are
implementation details to document before release.

The versioned artifact binds operation, Site/instance or standalone identity,
Environment, actual Host and Docker daemon, Compose project, effective
configuration and versions, selected deletion options, ordered resource identities,
preconditions, creation time, and expiry. Plans expire after 30 minutes. Preview
observes resources but never starts services, creates missing volumes, or deletes
anything; saving the requested artifact is its only intended write.

Show the selected resources and retained categories. Make media/download loss and
the absence of payload coverage in application-state backups prominent.
Production confirmation requires the full target identity. Production payload
deletion additionally acknowledges the target and selected loss categories;
explicit acceptance of potentially permanent loss is supported without an external
storage-backup validator. Disposable Staging payload deletion also requires
explicit loss acknowledgement. Automation requires a saved plan and explicit
confirmation/acknowledgement flags; it cannot skip the preview.

Recheck identity, ownership, storage boundaries, configuration, authorization, and
volatile prerequisites under the Environment lock immediately before execution.
Reject changed, expired, or modified artifacts. Uncertain ownership, an unexpected
volume consumer, symlink escapes, nested mounts, or ambiguous Host/daemon targeting
must block execution before stopping services. Filesystem traversal must stay
within the explicitly selected subtrees and must not follow links outside them.

## Backup and partial installations

Before volume deletion, automatically create and independently verify a protected
`before-destroy` backup of the final application state. Skip retention for this
operation. No supplied-backup age threshold substitutes for capturing that state.
Services must not resume writing between final-state capture and volume deletion.

Inventory existing resources first. Missing resources count as already removed;
do not recreate missing volumes in order to back them up. Capture exactly the
surviving owned volumes and explicitly record missing declared volumes. An
incomplete snapshot is labelled as such and must not pass as a complete Environment
backup to ordinary restore. Document how its surviving state can be recovered.
If no selected volumes exist, report that fact rather than inventing a complete
backup. Failure to protect any surviving selected volume blocks deletion.

When `--secrets` is selected, preserve the application document as ciphertext
alongside the protected destruction backup before removing its active copy.
Also preserve any active encrypted Restore Drill credentials needed to recover
the captured state. A missing document is reported as already absent. Do not
decrypt credentials merely to delete or archive their encrypted documents.
Keep artifacts private and never expose credential values in argv, output, plans,
logs, reports, or manifests. Volume archives remain secret-bearing state subject
to the existing access-controlled backup contract.

The existing backup archives application volumes, not media, downloads, or secret
documents. Its current declared-volume archive loop is insufficient for partial
installation handling; do not silently reuse it as proof of original existence
or complete application validity.

## Interruption and recovery state

Persist completed, failed, and pending actions in a durable operation journal
outside deleted resources. Support resuming the same operation after rechecking
the remaining identities and verifying its protected backup. Changed identities
or expired authorization require a new preview of remaining work. Missing targets
are idempotent; replacements at the same path/name are not silently authorized.

If backup preparation fails before deletion, attempt to restore the previously
running service set and report any recovery failure. After deletion begins, do
not claim automatic rollback or restart surviving services: leave them stopped,
retain the backup/journal, and report how to resume or recover.

Container-only destruction preserves restore and Profilarr recovery state.
Volume destruction refuses unfinished restore operations or unresolved Profilarr
recovery. It must not invoke restore recovery implicitly during preview.

After a successful volume reset, archive obsolete selected-Environment restore
and drill gates/journals and remove the active encrypted drill credential copy,
after preserving needed recovery ciphertext. Historical backups remain intact.
An interrupted reset must prevent apply or restore from interpreting obsolete
gates as permission to restart or resurrect deleted state. Retain the destroy
journal until its recovery-state transition is complete.

## Staging absent while Production persists

Production has no intended runtime dependency on Staging containers, application
volumes, payloads, or application credential documents. Preserve both Environment
declarations: current configuration validation requires them. Preserve shared VPN
credentials and decryption identities for Production apply and recovery.

Leave shared DNS and Traefik running; Staging routes may have unavailable backends.
Recreate Staging before new Staging verification or Restore Drills. A reset that
deletes application credentials requires initialization or credential recovery
before apply; retained credentials can be reused with fresh application volumes.

Historical Promotion evidence survives destruction: a completed test of an exact
change remains historical evidence. Configuration/version checks still apply.
Verification evidence expiry remains separate planned work; the current
verification report and Promotion check do not implement timestamp/expiry checks.
Destroy must not imply that they do.

## Delivery sequence and acceptance

| Phase | Work | Required evidence |
| --- | --- | --- |
| 1. Inventory and saved plans | Typed destroy request/options, owned-resource and filesystem inventory, Host/daemon confinement, private versioned artifacts, expiry and identity checks | CLI preview is observational; selectors/options validated; altered, stale, expired, wrong-Host/daemon plans refused; no secret values exposed |
| 2. Mutation coordination | Shared Environment lock across apply, backup, restore, and destroy; allow destroy's internal backup without self-deadlock | Conflicting commands cannot race; independent Environments remain independently operable; interruption releases the lock safely |
| 3. Final-state protection | Existing-volume snapshot, protected publication and verification, partial coverage metadata, ciphertext recovery copies | Missing volumes never created; backup failures block deletion and attempt service recovery; incomplete snapshots rejected as complete restores; no retention deletion |
| 4. Ordered execution and resume | Stop/remove containers, remove owned networks/runtime files, selected volume/payload deletion, credential removal, durable progress and recovery-state transitions | Every flag combination has the agreed effects; secrets require volumes; repeat/resume handles absent resources; injected failures after each phase preserve honest progress and stopped survivors |
| 5. Isolation and operator release | Human/JSON reports, documented approval/resume/recovery flow, updated CLI/runbook and relevant configuration-contract metadata | Disposable Docker acceptance proves Staging reset leaves Production resources, payloads, credentials, backups, and shared infrastructure untouched |

Safety acceptance must cover symlink/mount boundaries, unexpected consumers,
unknown resources retained, missing secrets/runtime files, partial installations,
active recovery journals, completed drill-gate retirement, cancellation, and
replaced resources between preview and execution. Use disposable fixtures and
Docker projects, never household Environments, for destruction tests.

Run the full repository suite with `./bin/media-stack test` or `go test ./...`
when implementation is complete, plus the explicit disposable Docker acceptance
cases. Documentation-only planning changes need consistency and diff checks;
they do not demonstrate implemented destroy behavior.
