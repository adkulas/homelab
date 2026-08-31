# Profilarr direct-database bootstrap

Research performed 2026-08-31 against the repository's pinned Profilarr image,
`ghcr.io/dictionarry-hub/profilarr@sha256:75a43c9c19c70f6e48315d4ed5cef3232d905da8fab397391a2078a5e0fd7ec1`.
GitHub Container Registry identifies that digest as Profilarr `2.1.0`; the corresponding upstream tag resolves to commit
[`395544b5dbd24c78f13a09226a87fe532ceb50d2`](https://github.com/Dictionarry-Hub/profilarr/commit/395544b5dbd24c78f13a09226a87fe532ceb50d2).
[Repository pin](../../stacks/media/versions.yaml)
[Official container package](https://github.com/dictionarry-hub/profilarr/pkgs/container/profilarr)

## Answer

Yes. Profilarr 2.1.0 stores its application state in SQLite at `/config/data/profilarr.db`. Arr connections are rows in
`arr_instances`, including name, type, internal URL, optional external URL, API key, tags, enabled state, library-refresh
state, and timestamps. The API key is stored directly in that table rather than represented by a separate secret reference.
[Upstream database documentation](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/docs/backend/database.md)
[Pinned schema](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/db/schema.sql#L16-L40)
[Database path](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/utils/config/config.ts#L127-L136)

Direct insertion is technically possible for this exact version, but it is not a safe or supportable automation seam. The
supported UI path performs validation and application work outside the bare insert: it restricts types to Radarr and Sonarr,
rejects duplicate normalized targets, parses tags, calls the query-layer create operation, may apply the configured default
delay profile to the Arr service, and logs the result. The create query itself also creates per-instance database-priority
rows for every linked PCD. A raw `INSERT INTO arr_instances` bypasses those behaviours.
[Create form action](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/routes/arr/new/%2Bpage.server.ts#L10-L137)
[Create query and priority side effect](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/db/queries/arrInstances.ts#L45-L72)
[Priority initialization](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/db/queries/arrSync.ts#L949-L962)

Writing the live file is especially unsafe. Profilarr opens SQLite in WAL mode and coordinates its own writes with
`busy_timeout` and `BEGIN IMMEDIATE`. Its restore implementation explicitly waits for a boot window with no open SQLite
handle because replacing database state while the process is running can leave the process reading inconsistent pages.
Any external database operation would therefore need Profilarr stopped, an atomic transaction, foreign keys enabled, correct
handling of the WAL/SHM files, a backup, and version-specific knowledge of all required side effects. Even then it would be
an unsupported coupling to migration internals, not a stable interface.
[SQLite connection setup](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/db/db.ts#L43-L90)
[Transaction implementation](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/db/db.ts#L160-L206)
[Upstream restore safety rationale](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/lib/server/utils/backup/applyPending.ts#L1-L17)

The lack of a supported bootstrap interface is explicit upstream. Profilarr 2.1.0's documented Arr API exposes only
`GET /api/v1/arr/instances`; it has no create operation. Upstream issue #308 requests environment-variable Arr credential
and instance bootstrapping, describes UI setup as the current workflow, and remains open. The UI does expose a private
connection-test route, but that does not turn the form action or database schema into a supported automation contract.
[Documented Arr API](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/docs/api/v1/paths/arr.yaml#L1-L27)
[Upstream bootstrap feature request](https://github.com/Dictionarry-Hub/profilarr/issues/308)
[UI connection-test route](https://github.com/Dictionarry-Hub/profilarr/blob/v2.1.0/src/routes/arr/validate/%2Bserver.ts)

## Recommendation

Keep the guided one-time UI step and verify its result through the documented `GET /api/v1/arr/instances` endpoint. Back up
the complete, quiesced environment-specific `/config` state afterward. Do not implement direct SQLite mutation in
`media-stack`: it would trade a visible one-time manual checkpoint for silent schema coupling, plaintext-secret handling,
missed initialization side effects, and upgrade risk.

Revisit this decision when Profilarr closes issue #308 or adds a documented Arr-instance create endpoint, import format, CLI,
or environment-variable bootstrap contract. A supported upstream interface should replace the manual checkpoint; the
database schema should not.

