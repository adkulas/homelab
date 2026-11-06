#!/usr/bin/env python3
"""Apply a reviewed v15.6.2 configuration export; never start DNS."""
import hashlib
import os
from pathlib import Path
import pwd
import subprocess
import sys
import tarfile
import tempfile
import uuid


def validate_configuration(root):
    if not (root / 'dns.config').is_file():
        raise ValueError('archive must contain dns.config at its root')
    for entry in root.rglob('*'):
        if entry.is_symlink() or not (entry.is_file() or entry.is_dir()):
            raise ValueError('configuration must contain only regular files and directories')
    scopes = root / 'scopes'
    if scopes.exists():
        for scope in scopes.iterdir():
            data = scope.read_bytes() if scope.is_file() else b''
            # Pinned Technitium v10 scope format: SC, version, name length,
            # UTF-8 name, enabled byte. Match the existing recovery adapter.
            if not scope.name.endswith('.scope') or len(data) < 5 or data[:3] != b'SC\x0a':
                raise ValueError('unsupported scope serialization')
            end = 4 + data[3]
            if end >= len(data) or data[end] != 0:
                raise ValueError('all restored DHCP scopes must be disabled')


def unpack(archive, digest, target):
    if hashlib.sha256(archive.read_bytes()).hexdigest() != digest:
        raise ValueError('archive checksum differs from the approved configuration')
    with tarfile.open(archive, 'r:gz') as stream:
        members = stream.getmembers()
        for member in members:
            path = Path(member.name)
            if path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir()):
                raise ValueError('unsafe archive member')
        stream.extractall(target, members=members, filter='data')
    validate_configuration(target)


def apply(archive, digest):
    state = Path('/var/lib/technitium-pi')
    if not state.is_dir() or state.is_symlink():
        raise ValueError('install the native service before applying configuration')
    account = pwd.getpwnam('technitium-pi')
    with tempfile.TemporaryDirectory(prefix='pi-config-', dir='/var/lib/pi-provision') as work:
        staged = Path(work) / 'configuration'
        staged.mkdir(mode=0o700)
        unpack(archive, digest, staged)
        for entry in [staged, *staged.rglob('*')]:
            os.chown(entry, account.pw_uid, account.pw_gid)
            entry.chmod(0o700 if entry.is_dir() else 0o600)
        # Validate fully before interrupting DNS. Preserve every previous state
        # including leases and externally installed TLS files for local rollback.
        subprocess.run(['systemctl', 'stop', 'technitium-pi.service'], check=True)
        subprocess.run(['systemctl', 'disable', 'technitium-pi.service'], check=True)
        previous = Path('/var/lib/pi-provision') / ('previous-' + uuid.uuid4().hex)
        state.rename(previous)
        try:
            staged.rename(state)
        except OSError:
            previous.rename(state)
            raise
        print('applied-stopped; previous configuration retained in private workspace')


if __name__ == '__main__':
    try:
        apply(Path(sys.argv[1]), sys.argv[2])
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, tarfile.TarError):
        sys.exit('Configuration application failed; inspect private state locally; DNS was not started.')
