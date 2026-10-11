# DuckDNS Stack

This Stack publishes the public IPv4 address detected by DuckDNS. A hardened oneshot systemd
service runs from a five-minute timer. Successful checks with no address change produce no application log entry.

The repository is the source of truth, but the service runs only from files installed under `/etc`, `/usr/local`, and
`/var/lib`. The DuckDNS token remains in the SOPS document at rest and is exposed only in the updater process environment.

## Configure and install

Edit the encrypted document and set `DUCKDNS_TOKEN` to the token from the DuckDNS account page:

The bootstrap token is empty, so installation refuses it until you fill it in. SOPS uses a temporary plaintext file when
editing: to keep that file in memory on Linux, run the editor with a private tmpfs temporary directory and disable editor
swap/backup files:

```bash
duckdns_edit_dir=$(mktemp -d /dev/shm/duckdns-edit.XXXXXX)
TMPDIR="$duckdns_edit_dir" SOPS_EDITOR='vim -n -i NONE -c "set nobackup nowritebackup noundofile"' \
  sops stacks/duckdns/secrets.sops.yaml
rmdir "$duckdns_edit_dir"
```

This requires Vim. `/dev/shm` is memory-backed; to exclude plaintext from disk-backed swap, disable swap or use encrypted
swap on the editing machine. The runtime updater never creates a decrypted file.

Save only the encrypted document to Git. The existing age recipient is shared with the Media Stack. Every Host with that
private identity can decrypt any document addressed to it; separate files organize secrets but do not isolate access.

The Host must have SOPS installed at `/usr/local/bin/sops` and the shared age identity installed at
`/etc/sops/age/keys.txt`, owned by `root:sops` with mode `0640`. Install or update the Stack from the repository root:

```bash
sudo ./stacks/duckdns/install.sh
```

The installer validates decryption as the unprivileged `duckdns` account before changing installed files. It enables and
starts `duckdns.timer`; it does not contact DuckDNS directly.

Starting the timer can trigger an update immediately when the Host has been up longer than one minute. Subsequent runs are
approximately five minutes apart. Each run contacts DuckDNS with `ip=&verbose=true`; its returned IPv4 is compared with
`/var/lib/duckdns/last_ip`. Only a complete successful response permits an atomic state replacement. No external IP lookup
provider is needed. See the [DuckDNS API specification](https://www.duckdns.org/spec.jsp).

`NOCHANGE` with the same saved IP is silent. If DuckDNS reports `UPDATED` while the saved IP already matches, the updater
logs a restored-address event: another writer may have changed the remote record. A missing state file logs initialization.

The shared identity must be readable by the `sops` group. Verify it on the Host:

```bash
sudo stat -c '%U:%G:%a %n' /etc/sops/age /etc/sops/age/keys.txt
# Expected: root:sops:750 directory; root:sops:640 key file
```

On the x86-64 `homelab` Host, install the same pinned SOPS release used by this repository:

```bash
ssh adam@homelab
sudo apt-get install curl ca-certificates
duckdns_download_dir=$(mktemp -d)
cd "$duckdns_download_dir"
curl -fLO https://github.com/getsops/sops/releases/download/v3.13.2/sops-v3.13.2.linux.amd64
curl -fLO https://github.com/getsops/sops/releases/download/v3.13.2/sops-v3.13.2.checksums.txt
sha256sum --ignore-missing -c sops-v3.13.2.checksums.txt && \
  sudo install -o root -g root -m 0755 sops-v3.13.2.linux.amd64 /usr/local/bin/sops
cd -
```

Then, from the repository checkout on `homelab`:

```bash
git pull
sudo ./stacks/duckdns/install.sh
systemctl list-timers duckdns.timer
sudo duckdns-logs
```

## Operate

Display address changes and failures from the last 30 days, or choose another journal-relative time:

```bash
duckdns-logs
duckdns-logs "7 days ago"
duckdns-logs "24 hours ago"
```

Users need permission to read the system journal; use `sudo duckdns-logs` if the current account does not have it. For an
intentional immediate update, run `sudo systemctl start duckdns.service`.

The journal still contains systemd's own service lifecycle messages; `duckdns-logs` selects only application events. Full
diagnostics remain available through `sudo journalctl -u duckdns.service`. Reinstall after editing the encrypted token.
The updater sends the token to curl through stdin, keeping it out of command arguments; HTTPS certificate verification
remains enabled. Root and processes permitted to inspect the updater can still access its environment. Encrypted-at-rest
configuration does not protect secrets from a compromised authorized Host.

## Test locally

```bash
bash -n stacks/duckdns/*.sh
bash stacks/duckdns/test.sh
```

Tests use a fake curl command and temporary state. They never contact DuckDNS. `--test-core` is the updater's explicit test
entry point; the installed service always uses its fixed runtime paths.
