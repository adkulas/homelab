# Mission: Operable, Shareable Home Infrastructure

## Why
Build a public repository that can operate the household's services with little routine intervention, while letting another person adopt the same opinionated system without inheriting private household details.

## Success looks like
- Recover or reproduce every managed service from versioned declarations, encrypted secrets, backups, and one documented workflow.
- Add a machine or network fact without hiding the reusable service design inside machine-specific files.
- Decide consistently whether a value belongs in public configuration, encrypted site inventory, or runtime discovery.
- Give a new operator safe defaults, validation, and clear required choices instead of a copy of this household.

## Constraints
- The repository is public.
- Sensitive values use SOPS and decryption identities remain outside Git.
- Ubuntu is authoritative, with Docker Desktop through WSL 2 also supported.
- Configuration should remain readable, reviewable, and easy to change.

## Out of scope
- Fully unattended GitOps reconciliation across every host.
- Treating every upstream application preference as managed configuration.
- Publishing an exact household network map merely to demonstrate portability.
