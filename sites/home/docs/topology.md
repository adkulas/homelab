# Proposed home network topology

The home Site Inventory declares `homelab-dns` on Host `homelab` as the primary DNS Stack Instance and `raspberrypi-dns` on Host `raspberrypi` as the secondary. The Site owns their DNS Cluster. These declarations do not install Technitium or change any Host address or DHCP service.

`site.yaml` and the two DNS Host declarations use semantic IDs. Their operational IP addresses and the selected device MACs are encrypted in `sites/home/secrets/site.sops.yaml`. The new file uses the existing age recipient, whose private identity is copied to `homelab`, `pc`, and the media laptop. Those copies are one identity; there is no independent recovery key. Keep the identity outside Git and retain an accessible copy if one Host fails.

## Proposed address roles

The subnet and gateway come from the current preferred LAN route, checked against the Bell device snapshot. The proposed static DNS Host addresses end in `.10` and `.11`. The excluded reservation band is `.20–.99`; `pc` is proposed at `.30` and the media laptop at `.31`, using their observed MACs. The `pc` MAC comes from the snapshot's `desktop` row; confirm that this row is the Minecraft PC before applying the reservation. The proposed dynamic band matches Bell's observed `.100–.254` pool. These are declarations for later staging, not active DHCP settings.

The [Bell snapshot](../secrets/devices.sops.yaml) has disconnected historical entries at `.10`, `.11`, and within the low band. It also has duplicate observed IPs. A snapshot cannot prove that an address is currently free. Compare the proposed addresses with current leases and fixed device settings, and confirm the wired/local-console recovery path before assigning a Host address or enabling DHCP. Keep Bell DHCP authoritative until the supervised cutover.

## Validate without exposing values

An operator with the existing age identity can run this read-only command from the repository root:

```bash
go run ./cmd/homelab site validate --site home --output json
```

If SOPS does not find the identity automatically, set `SOPS_AGE_KEY_FILE` to its private path. The JSON `networkPlan` contains semantic references and `<redacted>` markers. Diagnostics identify invalid entries without printing decrypted addresses or MACs. Validation checks the encrypted document and declared relationships; it does not contact the router or prove live address availability. Continue with the [read-only preflight](../../../services/network/docs/preflight.md) before any network change.
