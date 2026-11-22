---
name: media-stack-upgrade
description: Prepare immutable Media Stack image upgrades, rehearse a merged candidate on a selected Staging Host, or hand off Promotion with evidence and rollback steps.
---

Follow the phases authorized by the request. The [Media Stack upgrade runbook](../../../services/media/README.md#upgrading-image-pins) is the source for commands and evidence requirements.

1. **Prepare the update.** Use [Prepare the pin update](../../../services/media/README.md#prepare-the-pin-update) to produce the reviewed pin changes and release summary.
2. **Rehearse the candidate.** Use [Resolve the Host and freeze the checkout](../../../services/media/README.md#resolve-the-host-and-freeze-the-checkout), then [Rehearse in Staging](../../../services/media/README.md#rehearse-in-staging) to upgrade existing disposable Staging state and collect evidence.
3. **Hand off the rollout.** Use [Hand off Promotion and rollback](../../../services/media/README.md#hand-off-promotion-and-rollback) to report the evidence, preview status, and rollout and recovery commands. Perform the Production deployment phase only upon an explicit deployment request.
