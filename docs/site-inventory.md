# Site Inventory

Site Inventory declares where reusable Stacks run without joining them into one Compose project. A Site is an independent
administrative boundary. It explicitly references Host and Stack Instance documents beneath `sites/<site>/`; adding another
file never changes the Site by discovery.

The checked-in `sites/example` directory is the golden path. It contains illustrative values only and is validated through
the same command as every other Site:

```bash
CGO_ENABLED=0 go build -o bin/homelab ./cmd/homelab
./bin/homelab site validate --site example
./bin/homelab plan --site example --instance media --environment staging --output human
```

Both commands are read-only. Validation uses files only: it does not contact Docker, applications, the network, or SOPS.
Planning resolves one selected Stack Instance and Environment and delegates to that Stack's existing typed planner.

## Document contract

Every document uses `apiVersion: homelab.site/v1alpha1`. Unknown fields, unsupported versions and kinds, missing explicit
references, duplicate identifiers, and references that traverse or resolve through a symlink outside the selected Site are
rejected.

`site.yaml` has kind `Site`, a `metadata.name` matching its directory name, and these fields:

```yaml
spec:
    hosts:
        - hosts/media.yaml
    stackInstances:
        - instances/media.yaml
```

A kind `Host` has a unique `metadata.name` and capabilities from the v1alpha1 catalog:

- `containers.compose`
- `network.tun`
- `network.net-admin`

A kind `StackInstance` has a unique `metadata.name`, selects `stack: media`, binds one listed Host, declares a Host-scoped
`runtimeIdentity`, and supplies typed `media.defaults` and `media.environments` inputs. The adapter combines those inputs with
the reusable acquisition policy in `stacks/media/media-stack.yaml` and the checked-in image versions. The legacy
`media-stack` command remains available during migration.

Validation rejects a Media Stack Instance on a Host missing any of the three current capabilities. Published ports,
runtime identities, and overlapping data or backup roots must be unique among Stack Instances on one Host. Equivalent
resources on different Hosts are valid. Production and Staging remain distinct Environments within each instance.

Human and JSON output is deterministic. JSON documents use the same API version and the kinds `SiteValidation` and
`StackPlan`. Diagnostics have stable codes, explanations, and remedies.

## Sensitive Site topology

Low-impact identities, assignments, capabilities, and illustrative paths stay in cleartext. A Site that needs to hide exact
topology declares one confined SOPS document and separate daily and recovery age recipients:

```yaml
spec:
    sensitiveValues:
        document: secrets/site.sops.yaml
        dailyRecipient: age1daily...
        recoveryRecipient: age1recovery...
```

The decrypted document uses generic structural keys. Semantic identifiers are values rather than YAML mapping keys, so SOPS
metadata does not expose a household-specific schema:

```yaml
apiVersion: homelab.site/v1alpha1
kind: SiteSensitiveValues
spec:
    values:
        - id: staging-data-root
          value: /private/path
```

Encrypt only `value` fields so semantic IDs remain reviewable:

```bash
sops encrypt --encrypted-regex '^value$' \
  --age 'age1daily...,age1recovery...' \
  --input-type yaml --output-type yaml values.yaml > secrets/site.sops.yaml
```

Reference those IDs from the typed Stack input:

```yaml
media:
    sensitiveReferences:
        environments:
            staging:
                dataRoot: staging-data-root
                dataRootClaim: sha256:<canonical-data-root-digest>
                dataRootAncestorClaims:
                    - sha256:<proper-ancestor-digest>
                backupRoot: staging-backup-root
                backupRootClaim: sha256:<canonical-backup-root-digest>
                backupRootAncestorClaims:
                    - sha256:<proper-ancestor-digest>
```

`homelab plan` decrypts only when the selected Environment requires these values, keeps them in memory, and replaces them in
rendered output with `<redacted:<id>>`. Each claim is the lowercase SHA-256 digest of the cleaned absolute path, prefixed by
`sha256:`; ancestor claims contain the same digest for every proper ancestor except `/`. These non-sensitive claims let
offline validation detect equality and containment without decrypting exact paths. Planning recomputes them from the
decrypted value and rejects stale or dishonest claims before rendering. JSON lists each ID with a `<redacted>` value.
Decryption failures and invalid or
missing IDs use stable diagnostics and never include plaintext or ciphertext. Application credentials remain in the SOPS
documents named by each Media Stack Environment; Site Inventory does not absorb them. Databases, media, downloads, caches,
logs, and other runtime state remain under each Stack's backup and restore contract and never belong in Site Inventory.

Future Stack-to-Stack relationships must use typed outputs and inputs. They must not inspect another Stack's Compose file,
database, or private implementation.
