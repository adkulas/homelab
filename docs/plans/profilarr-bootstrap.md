# Issue 84 implementation and acceptance contract

Starting review point: `d9715a34464f7dc79e90728e39970ac4be502b22`.
The issue and the design interview settled in ADR 0009 are authoritative; the interview expands connection-only bootstrap
to pinned policy selections, initial/daily synchronization, and Seerr profile choice. No anime or separate-copy routing.

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| `media-stack apply` | Complete existing connections bypass private state | ADR 0009; issue stories 2–4, 26 | Successful repeated apply; no helper invocation |
| `media-stack apply` | Empty installation bootstraps automatically | Issue stories 1, 3, 15–18; ADR 0009 | Real pinned container, public connection API, Arr policy readback, downstream setup |
| `media-stack apply` | Partial, duplicate, unexpected state is preserved | Issue stories 5, 14; ADR 0009 | Stable non-fresh diagnostic; no mutation |
| Compatibility adapter | Exact actual image, schema, all migration names/versions | Reviewed v2.3.1 artifact, not reference schema.sql | Compatibility failures before mutation; disposable actual migrated volume |
| Compatibility adapter | Profilarr migrates itself and is quiesced before access | Issue stories 7–8; ADR 0009 | Public readiness, stopped PID, no other running volume owner |
| Compatibility adapter | Full backup, transaction, WAL handling, recovery journal | Issue stories 9–11; interview Q3/Q13 | Transaction/restart/verification/SIGTERM failure recovery; original state and archive retained |
| Compatibility adapter | In-memory keys only through stdin and SQL bindings | Issue stories 12–13 | Keys absent from output, Docker arguments, Compose, temporary payloads |
| Compatibility adapter | Exact pinned PCD and every connection's priorities | Issue story 16; interview Q14 | Checkout identity, public source state and actual synchronized profiles |
| Arr policy API | HD default, opt-in Remux 2160p Alternative, WEB-1080p series | Pinned PCD; interview Q5–Q8 | Profile qualities/scores/cutoffs, naming/size/media/delay readback |
| Profilarr public status API | First sync succeeds; daily synchronization from fixed source | Interview Q11–Q12 | Successful section timestamps/status; source auto-pull and update schedule disabled |
| Seerr supported API | New and existing household requesters can select profiles | Interview Q9–Q10 | Default permissions, preserved existing permission bits, idempotent apply |
| Environment selection | Credentials, state, journals, backups remain isolated | Issue stories 19, 24 | Separate backup directories and container/volume identities |

Compatibility fingerprints are derived from the database produced by the exact image, not by executing upstream reference
DDL. Profilarr's checked-in `schema.sql` is not the migrated schema (for example its delay-profile reference is stale).
The adapter uses Deno FFI with the image's bundled SQLite library, without downloading runtime dependencies.

The guided UI remedy remains available for unsupported versions. Non-stock PCD jobs also disqualify fresh state. SIGINT/SIGTERM trigger bounded recovery, including removal of an active
Docker helper. A leftover recovery journal blocks automatic application
before Profilarr startup. A recovered failure leaves Profilarr stopped, retains its archive, and reports that already-sent Arr
policy was not reversed. Live tests use only disposable volumes and HTTP Arr fixtures, with no public acquisition activity.
