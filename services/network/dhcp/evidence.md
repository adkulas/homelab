# Disabled DHCP staging evidence — #107

Disposable checks on 2026-11-07 used the repository's pinned Technitium v15.6.2
image and synthetic topology. No private home addresses or MACs are recorded here.

- Operator HTTP fixture checks passed: read-only protected plan, missing/stale
  approvals, backup corruption, endpoint binding, topology validation, scope options,
  reservation add/update/removal, idempotency, unknown ownership, overlapping scopes,
  unowned option protection, enabled allocation and retained-lease rejection.
- `HOMELAB_LIVE_DHCP=1 python3 -B -m unittest discover -s services/network/dhcp -p 'test_*.py' -k pinned -q`
  passed against an unpublished target on a fresh internal bridge. The real CLI
  applied the exact plan through scope-only restore, then verified an empty second
  plan, unchanged second apply, literal option/reservation readback and disabled
  persistence across target restart. The proxy rejected direct scope set/enable
  writes. Temporary target containers, volumes, bridge and credentials were removed.

- `services/network/pilot/rehearse.sh staged_dhcp_check.py` passed. The fresh pilot
  joined two nodes, provisioned home/reverse catalog zones, synchronized filtering
  and proved secondary expiry. Disabled scope import required no target set/enable
  call. After explicit pilot-only enable, reserved and dynamic clients received
  the expected addresses, gateway, both DNS endpoints and home/search suffix.
  A/PTR records appeared on both resolvers; explicit lease removal and natural
  one-minute expiry removed them. The isolated probe observed exactly one offer
  source. Pilot containers, volumes, bridge and credentials were removed.

The household-connected scope was subsequently staged disabled on 2026-11-08 UTC;
see the [commissioning record](../docs/dhcp-commissioning.md) for the real paired
backup, private topology readback, unchanged repeat apply and Bell-only household
offer check. Client/DNS collision protection remains a prerequisite to household
allocation; this work provides no household enable path.
