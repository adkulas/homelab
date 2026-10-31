# SSD and Passport backups

The operator authorized wiping the 1 TB WD My Passport attached to `homelab`.
It is provisioned as one ext4 filesystem, labeled `homelab-backup` and mounted
by UUID at `/mnt/passport`. The external mount uses `nofail` so an unplugged
Passport does not prevent boot. USB backup operations require the actual mounted
filesystem and fail if it is absent; they never write into its underlying SSD
mountpoint directory.

## What is protected

Encrypted Restic snapshots include `/etc`, `/root`, `/home/adam`, installed-package
and storage metadata, and the existing application-state archives beneath
`/mnt/backups/media`. Home and root cache directories are excluded. Media payloads,
downloads, and live Docker volumes are excluded.

There are two independent encrypted repositories:

| Device | Repository | Purpose |
| --- | --- | --- |
| SSD | `/mnt/backups/restic/homelab` | Fast local recovery |
| Passport | `/mnt/passport/homelab/restic` | Recovery after internal disk failure |

The primary application-state archive directories are `/mnt/backups/media/production`
and `/mnt/backups/media/staging`, matching the existing Media Stack configuration.
These archives stay on the SSD; Restic protects them in both repositories.

Host snapshots alone do not provide consistent application database backups.
No applications or application backup hooks were deployed during Host setup.
When the Media Stack is deployed, install root-owned executable hooks in
`/etc/homelab/backup.d` that invoke its existing `media-stack backup` command for
each deployed Environment, with the correct checkout and SOPS identity. Hooks
run before the local snapshot, and any hook failure stops that snapshot. Use
`run-parts`-compatible filenames without a dot, such as `20-media-production`.
Do not substitute a raw copy of running databases or Docker volumes.

Both repositories are on devices attached to the same machine. This provides
disk failure recovery, but an additional disconnected or remote copy is needed
for loss of the whole machine or writable-backup compromise.

## Schedule and operation

Times use the Host's America/Toronto timezone, with up to 15 minutes of randomized
delay. Persistent systemd timers catch up after downtime. Operations share a lock
to prevent local backup, copying, and integrity checks from overlapping.

| Timer | Schedule | Action |
| --- | --- | --- |
| `homelab-backup-local.timer` | Daily 02:00 | Run application hooks, snapshot to SSD, retain and check metadata |
| `homelab-backup-usb.timer` | Daily 03:00 | Copy snapshots to Passport, independently retain and check metadata |
| `homelab-backup-check.timer` | Sunday 04:00 | Read and verify all repository data on both devices |

Retention keeps seven daily, four weekly, and six monthly snapshots per backup
group. Copying does not mirror deletions from the SSD repository. The Passport
applies its own retention policy. An absent Passport can fail the USB service
without preventing SSD backups. Notifications need a separately configured
delivery destination; failures are currently visible in systemd and the journal.

From `adam@homelab`, run or inspect operations with:

```bash
sudo systemctl start homelab-backup-local.service
sudo systemctl start homelab-backup-usb.service
sudo systemctl start homelab-backup-check.service
systemctl list-timers 'homelab-backup-*'
sudo journalctl -u 'homelab-backup-*' --since today
sudo restic --repo /mnt/backups/restic/homelab \
  --password-file /etc/homelab/backup-password snapshots
```

Routine Ansible runs preserve both filesystems and repositories. The one-time
Passport initialization requires `--ask-become-pass`, `--tags backups`, and
`--extra-vars '{"host_initialize_passport": true}'`. Before wiping, the shared
disk safety checks require its exact hardware identity, capacity, original NTFS
filesystem UUID, and absence of mounted descendants. The default is false.

## Recovery password

The generated Restic password is root-only at `/etc/homelab/backup-password`.
Ansible encrypts a recovery copy to the existing Site age recipient and fetches
only that encrypted copy into [secrets/backup-password.age](secrets/backup-password.age).
Keep the encrypted recovery file with the repository and retain the matching age
private identity on another machine. The private identity is never stored in Git.
The optional `secrets` bootstrap copies the controller's existing identity to the
Host only when explicitly enabled. A copy of the repository on the failed
SSD alone is insufficient for recovery.

Both repositories use this password. Never replace it casually: use Restic's
documented key-management procedure and update the encrypted recovery material
together. Rotating the Site age recipient also requires re-encrypting the recovery
copy. Provisioning refuses to generate a new password if an existing
repository is found without its password file.

On another machine with the existing age identity, decrypt the recovery password
to a protected temporary file, then inspect or restore the Passport repository.
Adjust the mount path if that machine mounted the drive elsewhere:

```bash
umask 077
age --decrypt --identity "$HOME/.config/sops/age/keys.txt" \
  --output /tmp/homelab-backup-password \
  sites/home/hosts/homelab/secrets/backup-password.age
sudo restic --repo /mnt/passport/homelab/restic \
  --password-file /tmp/homelab-backup-password snapshots
sudo restic --repo /mnt/passport/homelab/restic \
  --password-file /tmp/homelab-backup-password \
  restore latest --target /srv/homelab-recovery
rm /tmp/homelab-backup-password
```

Inspect the recovered files before applying them to a rebuilt Host, particularly
disk mounts, network settings, SSH configuration, and private keys. Restore
application-state archives through the existing Media Stack restore workflow.

## Initial verification

The initial SSD snapshot was copied to the Passport, and both repositories passed
full data verification. Sample hostname and SSH configuration files restored from
the Passport matched the live originals byte for byte. A temporary USB unmount
confirmed that the backup runner rejects a missing mount without creating a
fallback repository on the SSD; the drive was then remounted. The encrypted
recovery password was decrypted with the controller's existing age identity and
compared to the Host password without displaying or storing plaintext locally.

No application Restore Drill was performed because no applications are deployed.

References: [Restic repository operations](https://restic.readthedocs.io/en/stable/045_working_with_repos.html)
and [retention](https://restic.readthedocs.io/en/stable/060_forget.html).
