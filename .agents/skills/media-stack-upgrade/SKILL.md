---
name: media-stack-upgrade
description: Update immutable Media Stack image pins, rehearse the merged upgrade in Staging, or prepare Promotion and rollback.
---

Run the phases the operator authorized. Use only the relevant section of the [upgrade runbook](../../../services/media/README.md#upgrading-image-pins).

1. [Prepare](../../../services/media/README.md#prepare-the-pin-update). Resolve stable tags and digests, review migration notes, and run the full Go suite once on the final local candidate. Save the log and watch candidate CI with one command.
2. [Rehearse](../../../services/media/README.md#rehearse-in-staging). Freeze the merged SHA and verify the selected Host. Run `upgrade preflight` with both legal fixtures and the baseline pins before taking the protected backup. Apply and repeat apply, then run `upgrade evidence` for the candidate. Report missing fixtures and the checks they block.
3. [Hand off](../../../services/media/README.md#hand-off-promotion-and-rollback). Use the evidence manifest for the Promotion preview. Report the target, SHA, failed or skipped checks, and evidence and rollback paths. Production apply requires explicit authorization.

Keep verbose output in files. Read failed check names and the relevant log excerpt. Report state changes and blockers when a long-running command completes.
