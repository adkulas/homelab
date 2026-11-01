# Automated companion first run

Source: the operator's follow-up to issue #88: automate the one-time companion
setup, let the operator choose usernames/passwords, and store them in SOPS.
Starting HEAD: `c2ce495faae5ee55caac728e4bd2aec60ad4fedb`.

The operator supplies credentials through local initialization prompts (passwords
hidden on a terminal) or the existing protected non-interactive answers file.
No credentials are generated. Initial credentials are encrypted before any
account is created. Existing accounts and their passwords are never reset.
`apply` uses the pinned services' account APIs and authenticated CSRF-protected
web forms; it does not edit upstream databases. Existing installations can add
missing credentials through `init --companions` without changing other secrets.

| Public seam | Required behavior | Source of truth | Evidence |
| --- | --- | --- | --- |
| init | Collect chosen Houndarr/Cleanuparr credentials, validate them, encrypt without displaying values | Operator input and upstream account limits | CLI secret-contract and interactive acceptance |
| init --companions | Add missing credentials to an existing selected SOPS document, preserving unrelated settings/recipients and existing credentials | Selected Environment secret document | CLI migration/idempotence and failed-write acceptance |
| apply | Create first-run accounts and Houndarr's local Sonarr connection/settings automatically, with searches paused until configuration is verified | Declared policy and supplied credentials | CLI external HTTP traffic and real-image acceptance |
| apply | Retrieve Cleanuparr API key, persist it encrypted, then reconcile connections/recovery | Supported account API and selected Environment secrets | No key in argv/output/Compose; rerun reuses key |
| apply | Resume interrupted first run; refuse authentication failures or ambiguous existing connections without resetting accounts | Persisted credentials and upstream state | Failure/retry and existing-account acceptance |
| plan / verify | Remain read-only and never create accounts or write secrets | Existing observation contract | CLI read-only acceptance |
| config describe / runbook | Describe automated first-run ownership and user-supplied credential lifecycle | Implemented behavior | Contract acceptance and operator documentation |
