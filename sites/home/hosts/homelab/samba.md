# Production media over SMB

Household clients can open `smb://homelab.home/media` with an individual SMB
login. The share contains `movies` and `series` from the Production Environment.
On Windows, enter `\\homelab.home\media` in File Explorer. Use the Host's LAN
address if the client lacks home DNS. Connections use direct SMB on TCP 445.

## Enable or disable

Set `host_samba_enabled` to `true` in [Host inventory](ansible/inventory.yml).
Use a controller with SOPS and the age identity for the encrypted Site Inventory.
Samba uses the same approved IPv4 LAN prefix as primary DNS and management.
From `sites/home/hosts/homelab/ansible`, install the pinned collections and run:

```bash
ansible-galaxy collection install -r requirements.yml
ansible-playbook -i inventory.yml playbook.yml --tags samba --ask-become-pass
ansible-playbook -i inventory.yml playbook.yml --tags samba --ask-become-pass
```

The second run should report `changed=0`. The full Host playbook also applies
this setting. Check mode defers enablement. A normal run checks that the existing
media filesystem is mounted and checks library access as `smb-media` before
installing the share. If the check fails, inspect directory traversal and file
read permissions with `namei -l`, `getfacl`, and `sudo -u smb-media`, when the
account exists. Agree on any permission fix after reviewing its effect on torrent
downloads and hardlinks. The playbook leaves library ownership, modes and ACLs in place.

Check `systemctl is-active smbd.service` and, privately on the Host, run
`sudo nft list table inet homelab_smb`. Confirm that TCP 445 accepts the approved
LAN source and drops other IPv4 and IPv6 sources. Test TCP 445 from the intended
LAN client and from an independently reachable source outside the approved range
where available. Inspect the accept and drop counters after those tests. Keep
TCP 445 off router port forwards.

Samba also restricts connections to the approved LAN range. If a separate firewall
operation clears its table, rerun the playbook to restore the managed rules.

Set `host_samba_enabled` back to `false` and rerun the playbook to stop and disable
Samba and remove its firewall table. Check `systemctl is-active smbd.service`
and `systemctl is-enabled smbd.service` for `inactive` and `disabled` respectively.
`sudo nft list table inet homelab_smb` should report that the table is absent.
The accounts remain registered and work again after re-enabling the share.

## Manage individual logins

Run these commands on `homelab` after enabling Samba. Choose one new, dedicated
Unix username per person and set their SMB password at the interactive prompt.

```bash
reader=smb-alex
sudo useradd --no-create-home --shell /usr/sbin/nologin --gid smb-media-readers "$reader"
sudo smbpasswd -a "$reader"
```

To revoke a reader, first remove their Samba password entry with
`sudo smbpasswd -x "$reader"`. Use `sudo smbstatus --processes` to find remaining
client processes for that username, and run `sudo smbcontrol PID shutdown` for
each matching PID. If a client connection is shared by multiple logins,
disconnecting it also interrupts the other logins on that connection. Then delete
the dedicated Unix account with `sudo userdel "$reader"` and verify the revoked
credentials fail in a fresh connection.

Keep the approved username roster in the operator's private records. After a Host
rebuild, enable Samba to recreate its service identity and reader group. Recreate
each still-approved login with the add commands above, issue a fresh password,
and update saved credentials on that person's clients. Passwords are entered on
the Host and kept out of inventory and Git.

## Verify playback and hardlinks

On the first household client, sign in with its individual SMB login, browse
`movies` and `series`, and play a file. On a LAN machine with `smbclient`, try a
guest listing and an authenticated write. Expect access or logon denied for the
guest and an access or media-write-protected error for the write.

```bash
smbclient //homelab.home/media -N -U '%' -c ls
smbclient //homelab.home/media -U smb-alex -c 'mkdir smb-write-check'
```

After enabling SMB, manually start one authorized torrent using the normal
Production Environment workflow. Wait for Radarr or Sonarr to import it, leaving
the torrent seeding. On `homelab`, substitute the actual download and library
file paths, using `series` in both paths for an episode.

```bash
stat -c '%d:%i %n' \
  '/srv/media/production/torrents/movies/<download-file>' \
  '/srv/media/production/media/movies/<imported-file>'
```

Both lines must have the same device and inode. Open or retrieve the newly
imported library file through SMB while the torrent is still seeding.
