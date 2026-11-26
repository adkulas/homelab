# homelab Host provisioning

The Host's [Site Inventory declaration](../homelab.yaml) remains separate from
its [Ansible playbook](ansible/playbook.yml). Run the playbook from a controller
with SSH access to `adam@homelab` and Ansible Core 2.20 or later. Install the
pinned Ansible collections and SOPS with the Home Site age identity on the
controller before running. Host key verification remains
enabled.

From the repository root:

```bash
cd sites/home/hosts/homelab/ansible
ansible-galaxy collection install -r requirements.yml
ansible-playbook -i inventory.yml playbook.yml --syntax-check
ansible-playbook -i inventory.yml playbook.yml --check --diff --ask-become-pass
ansible-playbook -i inventory.yml playbook.yml --ask-become-pass
ansible-playbook -i inventory.yml playbook.yml --ask-become-pass
```

The final run checks convergence: it should report no changes. Administration uses
password-based sudo; `--ask-become-pass` (`-K`) prompts for the server password.
Never store sudo
passwords, private keys, or decrypted Site addresses in this directory.

Check mode defers Docker bootstrap when Docker is absent, since it cannot
install the dependencies needed to validate the daemon configuration. Once
Docker is installed, check mode can inspect the Docker tasks too. A preview
is not a substitute for the first real run and a second convergence run.

The tags `storage`, `base`, `repo`, `power`, `docker`, `ssh`, `secrets`, `duckdns`,
`backups`, `traefik`, `homepage`, `samba`, and `security`
select areas. The OS and package
inspection preflight always runs. For example:

```bash
ansible-playbook -i inventory.yml playbook.yml --tags base --ask-become-pass
```

## Managed configuration

For owner access from outside the LAN, follow [Tailscale enrollment, DNS and NVR routing](tailscale.md).
Select the opt-in installation with `--tags tailscale`.

- Essential administration tools, age, checksum-verified SOPS 3.13.2, SMART
  monitoring, and LVM tools.
- Password-based sudo for `adam`. The `security` tasks remove the temporary
  `/etc/sudoers.d/90-adam-ansible` rule and direct `docker` and `lxd` group
  membership, preserving the standard Ubuntu `sudo` group and other memberships.
- The existing [DuckDNS Stack](../../../../services/duckdns/README.md), using its
  maintained updater, log helper, hardened service, five-minute timer, and encrypted
  configuration. The `duckdns` tag provisions the equivalent installer account,
  permissions, and files directly through Ansible. It validates an encrypted
  candidate configuration as the service account before replacing the runtime
  configuration, and keeps the candidate encrypted at `/etc/duckdns/candidate.sops.yaml`.
  A service-readable copy of the shared identity is kept at `/etc/sops/age/keys.txt`
  as `root:sops` mode `0640`, with a `0750` parent directory. The dedicated
  `duckdns` account belongs to `sops`, matching the Stack's existing trust model.
- Git and an administrator-owned checkout of the public repository at
  `/home/adam/homelab`, cloned from `main` over HTTPS. The `repo` tag bootstraps
  the checkout; routine runs preserve it without pulling or overwriting work.
- Lid closure is ignored on battery, external power, and while docked through
  `/etc/systemd/logind.conf`. The `sleep`, `suspend`, `hibernate`, and `hybrid-sleep`
  targets are masked. Use `--tags power` to reconcile just these settings; they
  are also included in `base`. Logind restarts only if the lid settings change.
- One ext4 partition spanning the old 1 TB disk, mounted by UUID at `/srv/media`,
  with isolated `production` and `staging` directories owned by `adam`.
  Docker requires the media mount before starting.
- America/Toronto timezone, the existing chrony time service, and periodic SSD TRIM.
- Automatic security updates using Ubuntu's unattended-upgrades policy, with
  automatic reboots explicitly disabled.
- System journal limits of 500 MB persistent and 100 MB runtime, with 14-day retention.
- Docker Engine and Compose from Docker's signed stable Ubuntu package repository.
  Existing conflicting runtimes stop provisioning for review. Normal runs install
  missing packages without requesting upgrades to installed packages.
- The `docker` and `media-stack` tags install the systemd boot recovery template.
  A successful `media-stack apply` with sudo enables the selected Compose project.
  For unattended decryption with the provisioned Site identity, run apply with
  `sudo env SOPS_AGE_KEY_FILE=/etc/sops/age/keys.txt` before the CLI command.
  Inspect `journalctl -b -u media-stack-recover@media-production.service` after boot.
- Docker JSON logs limited to three 10 MB files per container. Other daemon
  configuration is preserved. Logging defaults apply to newly created containers;
  existing containers need recreation to adopt them.
- Container administration requires `sudo docker ...`; `adam` does not have
  direct Docker socket access. Run the Media Stack CLI with sudo when it needs
  container administration. Existing sessions need a logout and fresh login after
  group removal.
- SSH public-key authentication with password, keyboard-interactive, and direct
  root login disabled. The existing authorized key file is required. The complete
  SSH configuration is validated before reload. Existing Ubuntu socket activation
  is preserved.

Docker and journald restart only when their managed configuration changes; Docker
configuration changes can interrupt running containers. Reboots are reported and
require an explicit maintenance flag.

To upgrade installed packages deliberately during maintenance:

```bash
ansible-playbook -i inventory.yml playbook.yml --tags base \
  --extra-vars '{"host_upgrade_packages": true, "host_reboot_if_required": true}' \
  --ask-become-pass
```

## Initial inspection

The read-only inspection on 2026-10-30 (Toronto time) found Ubuntu 26.04.1 LTS
on the approximately 512 GB SSD, an Intel i5-7200U, approximately 8 GB RAM, and
an available Intel render device. Both drives passed SMART health checks.
The operator authorized wiping the old approximately 1 TB SATA disk, including
its OS and media volume. It was reformatted and mounted at `/srv/media`.
The SSD retains Ubuntu, Docker runtime state, and application configuration volumes.

The original boot network check timed out at 22:47:18 before the wired interface
gained carrier at 22:47:58. Both exact Netplan-generated readiness commands later
passed. Starting the service cleared the stale failure without changing network
configuration. A global wait command also waits for unused Wi-Fi and is not the
correct readiness test for this Host's configured boot service.

Initial provisioning applied OS updates and a controlled reboot to kernel
`7.0.0-42-generic`. After reboot, SSH, the media mount, Docker, chrony, and SMART
monitoring were verified, with no failed systemd services and no pending reboot.
The data filesystem provides approximately 916 GiB usable space. Docker Engine
`29.9.2` and Compose `v5.6.1` were observed after setup; their package versions
are not pinned by this playbook.

The full provisioning rerun reported zero changes and zero failures. Ansible lint
passed, and a read-only negative test confirmed that the storage guard rejects
the running OS disk. Backup recovery and integrity verification are recorded in
[backups.md](backups.md#initial-verification).

## Storage initialization and recovery

Routine runs never format a disk. `storage.yml` requires the existing managed
ext4 filesystem. If it is absent, provisioning stops unless the destructive
initialization flag is explicitly supplied. The hardware ID, serial number,
capacity, absence of mounted descendants or swap, and the old volume group's
physical volume identity must all match before the old disk can be erased.

The one-time initialization command used for this machine was:

```bash
ansible-playbook -i inventory.yml playbook.yml --tags storage --ask-become-pass \
  --extra-vars '{"host_initialize_storage": true}'
```

Do not use that flag in scheduled runs. A replacement disk requires a new reviewed
identity and initialization procedure. An interrupted initialization can require
manual recovery: the safety checks intentionally reject an unexpected partial
layout instead of trying another erase. The completed filesystem is recognized
without reformatting even if the initialization flag is accidentally repeated.

The mount is required at boot; it deliberately does not use `nofail`, so storage
failure cannot silently cause media writes to the SSD. Console recovery may be
needed if the disk is unavailable. The mount task also refuses to hide existing
files or replace an unrelated mount at `/srv/media`.

## Remaining setup

The existing controller age identity was copied to
`/home/adam/.config/sops/age/keys.txt` with mode `0600` and a `0700` parent directory.
Private key contents are never stored in this repository or displayed by Ansible.
Reproduce this explicit bootstrap from a controller holding the identity with:

```bash
ansible-playbook -i inventory.yml playbook.yml --tags secrets --ask-become-pass \
  --extra-vars '{"host_provision_age_identity": true}'
```

The source defaults to the controller's `~/.config/sops/age/keys.txt`; override
`host_age_identity_source` for another path. Routine runs skip key copying. An
existing server identity is preserved and must match the Site recipient.

Application deployment still needs the repository's CLI and Environment
credentials. The SSD and Passport backup configuration and
recovery procedure are documented in [backups.md](backups.md).
Use the existing Media Stack CLI to own application reconciliation rather than
duplicating it in Ansible. Start with Staging, including hardware transcoding
verification, before Promotion to Production.

The optional [Samba share](samba.md) manages a dedicated Host firewall table for
TCP 445 using the approved home LAN range. Docker's published ports need their
own firewall policy. This playbook deploys the Site Traefik container and publishes
TCP 80 and UDP 19134. Media application deployment uses the Media Stack CLI.
Disk health alert delivery also needs an agreed destination.

Host addressing, DNS, DHCP, and router changes follow the separate exact-plan
approval required by [ADR-0007](../../../../docs/adr/0007-gate-high-impact-network-changes.md)
and the [network setup procedure](../../../../services/network/docs/setup.md).

References: [Docker's Ubuntu installation instructions](https://docs.docker.com/engine/install/ubuntu/)
and [Ansible check mode](https://docs.ansible.com/projects/ansible/latest/playbook_guide/playbooks_checkmode.html).

## Traefik deployment

The `traefik` tag deploys the existing `home-traefik` Compose project from the
**controller checkout**, into a root-owned `/opt/homelab` tree. It copies only
`services/traefik/{compose.yaml,traefik.yml}`, `sites/home/traefik/compose.yaml`,
and the Site dynamic YAML files. The relative layout preserves Compose extends
and bind mounts. It does not read, pull, or update `/home/adam/homelab`.

From the repository root:

```bash
cd sites/home/hosts/homelab/ansible
ansible-galaxy collection install -r requirements.yml
ansible-playbook -i inventory.yml playbook.yml --tags traefik --check --diff --ask-become-pass
ansible-playbook -i inventory.yml playbook.yml --tags traefik --ask-become-pass
ansible-playbook -i inventory.yml playbook.yml --tags traefik --ask-become-pass
```

The last run should report zero changes. Docker Engine and Compose v2.18 or later
must already be available with sudo (`--tags docker` provisions them). A full run
applies pending Docker handlers before Traefik. A tagged run fails clearly if
prerequisites are missing. No administrator Docker group membership is granted.
Check mode never reconciles containers. On a fresh Host it explains deferred
installation/validation; on an installed Host it previews file changes and reports
which runtime checks require a normal run.

Commit or deliberately select the controller files before running. For rollback,
create a separate controller checkout at the desired commit (`git worktree add
--detach /tmp/homelab-traefik-rollback <commit>`), install that revision's pinned
collections, and run the same commands from its Ansible directory. Review the diff
first. This deliberately redeploys the older revision without touching the Host's
administrator checkout. Uncommitted controller edits are deployed too.

Re-run the tag for configuration updates. Dynamic YAML changes use Traefik's file
watcher; files previously managed by Ansible are removed when absent from the
selected checkout. Unrelated files are preserved. The deployment keeps a managed
filename manifest; do not edit that manifest. Static configuration changes force
container recreation because atomic replacement of a single-file bind mount
otherwise leaves the old inode mounted. The last successfully applied checksum
is retained so a failed run can retry recreation. Compose reconciles service
changes; routine runs pull only a missing pinned image and do not upgrade it.
Compose validation must pass before any container start or update.

Verification first checks the running container, then sends an unmatched Host
request to localhost HTTP and requires Traefik's 404. This proves proxy startup,
not application reachability. Inspect the installed project with:

```bash
cd /opt/homelab/sites/home/traefik
sudo docker compose config
sudo docker compose ps
sudo docker compose logs --tail 100 traefik
curl --noproxy '*' -i -H 'Host: traefik-startup.invalid' http://127.0.0.1/
# Actual Media route: expect the application's response when its backend is running.
curl --noproxy '*' -i -H 'Host: jellyfin.home' http://127.0.0.1/
# Check client DNS and the complete client path separately.
getent hosts jellyfin.home homelab.home pc.home
curl --noproxy '*' -i http://jellyfin.home/
# Container-side backend name resolution (independent of client DNS):
sudo docker compose exec traefik nslookup homelab.home
sudo docker compose exec traefik nslookup pc.home
```

A 502/504 on a matched route indicates an upstream problem; an unexpected 404
suggests a missing route or Host header. Check Traefik logs for dynamic-file parse
errors. A failed localhost startup check or stopped container is a proxy failure.
A DNS failure is a separate prerequisite. The container must resolve `homelab.home`
and `pc.home`; household clients must resolve the service aliases to Traefik.
See the [Site DNS requirements](../../README.md#name-resolution). Do not change
DNS, DHCP, Host addressing, or router settings as part of this deployment.

Minecraft is separate from the HTTP probe: `minecraft.home` must resolve to
homelab, `pc.home` must resolve inside Traefik, and the Bedrock process and PC
firewall must allow UDP 19134 from homelab. Verify with a Bedrock client against
`minecraft.home:19134` when those prerequisites exist; a UDP port scan alone does
not prove backend availability. Report it unavailable/unverified independently
of proxy startup. These tasks neither provision Minecraft nor enable a dashboard.
Keep published ports on the trusted LAN with no internet forwarding. TLS and
firewall rollout remain outside this deployment.

Local validation uses `tests/run.sh` in the Ansible directory. It builds an isolated
Ansible controller, uses a temporary deployment tree and ports 28080/29134, and
refuses to run if `home-traefik` already exists on the local Docker daemon. It
requires Docker, outbound access for test-tool installation, and free test ports.
It does not deploy to the live Host; record live deployment and backend checks
separately from this rehearsal.

## Homepage deployment

[Homepage](http://homepage.home) runs on `homelab` as the `home-homepage`
Compose project. Traefik reaches it on `home-traefik_default`. Only Traefik
publishes an HTTP port. Use the exact URL on the trusted LAN and keep it off
internet forwarding. HTTP carries the Homepage password and session cookie
without encryption. Use a unique Homepage password.

The [dedicated SOPS document](../../secrets/homepage.sops.yaml) uses the Home
Site age recipient. Its `password` and `session_secret` fields are required.
The session secret must contain at least 32 characters. View the password
privately with `sops decrypt --extract '["password"]' sites/home/secrets/homepage.sops.yaml`
from the repository root. Use `sops edit sites/home/secrets/homepage.sops.yaml`
to rotate credentials or enroll widget keys. Commit only the encrypted document.
Changing the session secret signs out existing sessions.

Provision Docker before the first run. Homepage needs Docker Compose 2.30 or
later for the literal authentication environment. Run from the repository root
with the pinned Ansible collections installed. The Host's default sudo-rs
wraps the password prompt. These commands select the installed `sudo.ws`
binary so Ansible can recognize the prompt.

```bash
cd sites/home/hosts/homelab/ansible
ansible-playbook -i inventory.yml playbook.yml --tags traefik,homepage --check --diff -K \
  -e ansible_become_exe=/usr/bin/sudo.ws
ansible-playbook -i inventory.yml playbook.yml --tags traefik,homepage -K \
  -e ansible_become_exe=/usr/bin/sudo.ws
ansible-playbook -i inventory.yml playbook.yml --tags traefik,homepage -K \
  -e ansible_become_exe=/usr/bin/sudo.ws
```

The second deployment should report zero changes. The `traefik` tag installs
the watched route and creates the shared network. After initial deployment,
use `--tags homepage` to apply credential and dashboard updates. Check mode
validates the encrypted input and previews file changes. Use a normal run to
validate Compose and the sign-in redirect. The installed project is under
`/opt/homelab/sites/home/homepage`. Inspect the containers with
`sudo docker compose --project-directory /opt/homelab/sites/home/homepage ps`.
An unfiltered `docker compose config` prints the authentication environment.

Reconcile `homepage.home` using the [declared DNS procedure](../../../../services/network/docs/declared-dns.md).
Review the plan for the new CNAME targeting `homelab.home`, apply it through the
primary DNS Stack Instance and verify the answer from both LAN resolvers. Check
that the browser opens `http://homepage.home` and redirects to the sign-in page.

### Enroll and verify widgets

Each optional field enables one widget. Omit the field to keep the link without
its widget. Widget requests use the Production ports declared in Site
Inventory. The Compose project must be able to resolve `homelab.home`.

| SOPS field | Credential source | Expected data |
| --- | --- | --- |
| `jellyfin_api_key` | Jellyfin Administration Dashboard → API Keys | Current title, elapsed time and progress, viewer name, and pause/resume controls |
| `seerr_api_key` | Seerr Settings → General → API Key | Pending, approved, and completed request counts |
| `qbittorrent_api_key` | qBittorrent Options → WebUI → Authentication → API Key | Download and upload rates and downloading and seeding counts |
| `sonarr_api_key` | Sonarr Settings → General → API Key | Series queue count |
| `radarr_api_key` | Radarr Settings → General → API Key | Movie queue count |

The qBittorrent key also enables a transfer summary container within the
Homepage Compose project. It uses qBittorrent's global rates and counts
torrents in the downloading and uploading states, including forced
transfers. The container has no published ports and shares its Docker
network only with Homepage. Removing the key and redeploying removes
the widget and its transfer summary container.

Verify the links-only deployment before adding keys. In a private browser
window, confirm that both the dashboard and `/api/services` redirect to sign-in.
A signed-out widget request to `/api/services/proxy` should also redirect to
sign-in. Sign in with the Homepage password and open all seven Production media
links plus the primary and secondary Technitium links. The Technitium links
redirect to the existing HTTPS consoles and keep their existing access controls.

Enroll the five keys and redeploy with `--tags homepage`. Compare each widget
with the corresponding application's activity and queue data. Start a Jellyfin
test playback, confirm its viewer and progress, and pause and resume it from
Homepage. Anyone with the Homepage password can see viewer names and operate
the playback controls. Sign out and retry the widget request to confirm that
the data is protected after sign-out.
