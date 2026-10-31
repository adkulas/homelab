# Series acquisition and queue recovery (#88)

Source: https://github.com/adkulas/homelab/issues/88

Implementation starting HEAD: `08787534f4ffcad1f0fd6d3a2501119045ef3ccd`.
The unrelated Host provisioning commit `634eca9` landed while work was interrupted.

## Agreed policy

Each Environment has its own policy and service state. Houndarr searches batches of
12 episodes every 30 minutes, with a 12/hour cap, one-day per-episode cooldown,
six-hour release grace, allowed window 06:00–00:00 in the container timezone, and
backpressure at two Sonarr queue entries. Chronological means original air date,
with upstream page rotation and no ordering guarantee for equal dates. Requests
join the shared missing-episode backlog; a new request is not a priority queue.
Sonarr RSS monitoring continues at all hours independently of these controls.

Cleanuparr observes every five minutes and requires 12 qualifying observations.
Slow speed below 100 KiB/s OR remaining time above 24 hours can each accumulate
strikes. Stall/slow strikes reset on progress. Metadata and failed-import strikes
are cumulative upstream. Release-specific import faults qualify; infrastructure
faults require operator repair. Fully deselected torrents are the agreed exception:
Cleanuparr immediately removes those without waiting for strikes.

## Behavior matrix

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| plan | Pinned, isolated companions; loopback management; no media mounts or rendered credentials | versions.yaml and Environment policy | Rendered Compose acceptance |
| config describe | Search/cleanup ownership, semantic tuning, sensitivity, bootstrap and upstream exceptions | Service Configuration Contract | JSON and human contract acceptance |
| apply | Seerr monitors requests without grouped search; one proactive owner | Issue #88 and agreed policy | External Seerr, Houndarr and Cleanuparr settings |
| apply / plan | Guided Houndarr bootstrap and drift correction without database edits | Houndarr v1.13.3 authenticated UI | Authenticated form observation and connection test |
| apply | Reconcile Cleanuparr API settings, replacement enabled, proactive Seeker disabled, repeated strikes | Cleanuparr v2.10.12 API | External API traffic, idempotence and drift acceptance |
| verify | Distinct configuration, credential, dispatch, budget, cleanup and recovery diagnostics | Observed service APIs and Sonarr command/blocklist/queue state | Machine-readable diagnostics and disposable Staging evidence |
| backup / restore | Inventory both mutable states; keep recovered integrations gated until local targets are revalidated | Rendered named volumes and Restore Drill gate | Backup/restore acceptance |
| Staging Promotion | Multi-episode request, eligible-only episode commands, budgets, queue health and replacement context | Real pinned companions with legal fixtures | Opt-in disposable acceptance; never manufacture passing evidence |

## Upstream references

- [Houndarr settings](https://github.com/av1155/houndarr/blob/v1.13.3/website/docs/reference/instance-settings.md)
- [Houndarr ordering](https://github.com/av1155/houndarr/blob/v1.13.3/website/docs/concepts/search-order.md)
- [Cleanuparr queue evaluation](https://github.com/Cleanuparr/Cleanuparr/blob/v2.10.12/code/backend/Cleanuparr.Infrastructure/Services/QueueRuleEvaluator.cs)
- [Cleanuparr removal](https://github.com/Cleanuparr/Cleanuparr/blob/v2.10.12/code/backend/Cleanuparr.Infrastructure/Features/DownloadRemover/QueueItemRemover.cs)

Houndarr's queue inspection fails open. Cleanuparr's strike policy tolerates short
outages while below threshold, but is not a shared-connectivity circuit breaker.
Replacement dispatch uses the Seeker interval after successful removal/blocklisting;
single episodes use EpisodeSearch and grouped seasons use SeasonSearch.


## Validation record

The disposable pinned-image runs proved Houndarr episode context, eligible-only
selection, the 12-command hourly cap, queue backpressure, persisted cooldowns,
quiet hours and release grace. Cleanuparr's stalled, metadata, slow-speed,
slow-time and failed-import paths each reached 12 observations, removed and
blocklisted the fixture, and dispatched an episode replacement. Additional runs
proved client-outage retention, rejection of replacements after a failed
blocklist API call, and a SeasonSearch followed by scheduled episode fallback.
These runs used isolated legal HTTP fixtures, not household services or public
indexers. Real Seerr-to-media playback remains a separately opt-in disposable
Staging check.

Review additionally requires commands to follow the matching removal event and,
for pack fallback, the matching SeasonSearch. CLI regression cases reject earlier
proactive commands and premature/partial below-threshold searches.
