# Deployment-scoped Media Stack credentials

The implementation source of truth is the accepted conversation proposal: application
credentials belong to a Site's Stack Instance and Environment; NordVPN credentials
are deliberately shared within that Site. Host placement does not own application
identity. Existing credentials must survive migration without rotation.

| Public seam | Required behavior | Source of truth | Observable evidence |
| --- | --- | --- | --- |
| Media Stack CLI selection | Operational commands select Site, instance and Environment; no implicit global secret fallback | Accepted proposal | Missing/ambiguous selectors fail; selected instances resolve distinct credentials |
| Site validation and planning | Confine secret references to the selected Site; reject shared application documents and same-Host Compose project collisions | ADR-0004 and accepted proposal | Invalid declarations fail; equivalent resources on different Hosts are permitted |
| Initialization and apply | Encrypt instance/Environment ownership; reject mismatches; generated writes affect only the selected application document | Accepted proposal | Copied credentials fail before Docker; repeated initialization preserves existing values; shared VPN file is unchanged |
| Runtime secret materialization | Separate runtime directories by Site, instance and Environment | Accepted proposal | Rendered Compose uses distinct runtime file paths |
| Migration | Split legacy credentials into Site VPN and owned application documents without rotating secrets or overwriting destinations | Accepted proposal | Decrypt/compare round trip; missing legacy files allowed; divergent VPN values fail before writes |
| Backup, promotion and restore | Preserve effective configuration identity and destination credential isolation, including Restore Drills | Existing recovery contract and accepted proposal | Scoped command coverage; wrong-owner drill credentials rejected |

Supported legacy explicit `--config` inputs remain an escape hatch for migration and
existing standalone installations. Repository defaults never silently select them.
Reusable acquisition policy stays under `services/media/`; deployment inputs move
to Site Inventory. Shared VPN credentials use a separate Stack credential document,
not the Site-sensitive network inventory.

Review baseline: `413b52768014c232c633dda8acf93a6bb66a3468`.
