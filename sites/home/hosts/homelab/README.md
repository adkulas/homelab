# homelab Host provisioning

The Host's [Site Inventory declaration](../homelab.yaml) remains separate from
its [Ansible playbook](ansible/playbook.yml). Run the playbook from a controller
with SSH access to `adam@homelab` and Ansible Core 2.20 or later. Install the
pinned Ansible collections before running. Host key verification remains
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
`backups`, `traefik`, and `security`
select areas. The OS and package
inspection preflight always runs. For example:

```bash
ansible-playbook -i inventory.yml playbook.yml --tags base --ask-become-pass
```

## Managed configuration

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

Firewall configuration needs confirmed management and trusted LAN ranges and
must account for Docker's published-port rules. This playbook does not install
or enable a firewall. It deploys the Site Traefik container and publishes TCP 80
and UDP 19134; Media application deployment remains with the Media Stack CLI.
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
