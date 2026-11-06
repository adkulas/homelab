# Raspberry Pi provisioning

Run from an administrator workstation with Ansible installed, SSH key access and sudo
access to the Pi. The target must already boot the piloted Debian 13 ARM64 OS and have
Python 3. Flashing an SD card, enabling SSH and establishing access happen first.
Inventory uses the existing SSH alias `rasp-pi`; override connection details in a private
inventory outside Git. No operational addresses or credentials belong in these files.

From this directory:

```bash
ansible-playbook -i inventory.yml playbook.yml --ask-become-pass --check
ansible-playbook -i inventory.yml playbook.yml --ask-become-pass
```

The default `base` operation installs runtime dependencies and administration tools.
It is repeatable, does not upgrade all packages or reboot, and changes no addressing,
SSH policy, router settings or DNS configuration.

## Fresh native installation

Follow [secondary DNS preparation](../../../../../services/network/docs/secondary-dns.md)
through the bundle staging step, running `native.py stage` on the controller using the
pinned downloaded archives and privately reviewed ACLs. Set a private variables file:

```yaml
pi_action: install
pi_bundle_source: /absolute/private/pi-bundle
```

Then run:

```bash
ansible-playbook -i inventory.yml playbook.yml --ask-become-pass -e @/absolute/private/pi.yml
```

The existing installer checks the bundle runtime, firewall syntax and fresh-state paths.
It installs stopped and disabled. Repeating `install` refuses an existing installation;
use `base` for repeatable Host preparation. This is not an upgrade mechanism. Finish
credentials, preflight, HTTPS, supervised first start and soak using the secondary DNS
guide. The controller and the staged bundle must be trusted. Preserve the exact reviewed
bundle until installation is complete.

## Restore and configuration replacement

These operations replace the complete persisted configuration, leaving DNS **stopped
and disabled**. They are maintenance operations on this one Pi, not a live DNS settings
reconciler. Use them only after reviewing the exact archive and arranging recovery access.

For restore, first follow the [backup recovery guide](../../../../../services/network/docs/backup-restore.md)
on the recovery workstation: select the `raspberrypi` archive, create a `live` restore
plan, review it, and run the isolated restore. That exports full original node settings
with DHCP scopes disabled. A `drill` export is not a replacement for full node recovery.
Fence the old instance before reusing its identity. Install the native service first on
a replacement Pi; retain the same pinned Technitium version.

For a new configuration, prepare and verify a complete v15.6.2 configuration directory
on an isolated instance, stop it before exporting, and verify disabled scopes. This
preserves all settings in that export; bootstrap environment edits cannot reconcile an
existing installation. Routine DNS record changes and DNS Cluster configuration remain
with their existing procedures; this playbook does not implement API plan/apply.

Package the **contents** of the reviewed export on the controller:

```bash
umask 077
tar -czf "$CONFIG_ARCHIVE" -C "$RECOVERED" .
sha256sum "$CONFIG_ARCHIVE"
```

Keep this plaintext archive and its variable file outside Git on private storage. Set:

```yaml
pi_action: restore  # or configure for a reviewed replacement configuration
pi_configuration_archive: /absolute/private/pi-configuration.tar.gz
pi_configuration_sha256: REPLACE_WITH_REVIEWED_64_CHARACTER_SHA256
```

Run the same `ansible-playbook` command above with this private variable file. The helper
checks the approved SHA-256, rejects unsafe archive members and enabled/unknown DHCP
scope formats, stops/disables DNS, retains the entire previous directory under the
root-only `/var/lib/pi-provision/previous-*` path, and installs the new directory with
service ownership and private permissions. Reapplying performs another replacement and
retains another previous directory; configuration replacement is deliberately explicit.
It does not automatically restart DNS. Check mode previews file/package work but does
not validate or apply the archive or execute the native installer.

External TLS/app files absent from the export must be restored separately at their
original paths. Confirm `/etc/technitium-pi/admin-password` exists and remains private;
it is required by the unit even when authentication was restored. Firewall ACLs remain
those from installation; changing them requires separately reviewing the firewall plan.
Verify HTTPS, authentication, DNS, disabled DHCP and cluster identity privately using the
secondary guide before supervised start/enable. No other DNS Host should be restarted.

If apply fails, inspect the private workspace before retrying. For rollback, keep DNS
stopped, move the failed current directory aside and move the selected `previous-*`
directory back to `/var/lib/technitium-pi` as root. Inspect ownership and run the same
supervised acceptance checks before starting. Never merge old and new directories.
Retained configurations contain secrets; encrypt/offload them before deleting local
copies under your backup retention policy. The provisioning archive is retained mode
0600 for diagnosis; remove it after successful verification.

Developer checks:

```bash
python3 -B -m unittest discover -s sites/home/hosts/raspberrypi/ansible -p 'test_*.py'
```
