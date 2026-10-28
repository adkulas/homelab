#!/usr/bin/env python3
"""Stage verified native Pi DNS artifacts without changing the Host or starting services."""
import argparse
import hashlib
import ipaddress
import os
import shutil
import tarfile
from pathlib import Path
import sys

APP_SHA256 = '51f2d4ebaf156a85035e820afde5e1d428f1920a37d7f0c65203fb1766917e08'
RUNTIME_SHA512 = '7f70bf12f71d0949370b5d6681ad9702d13f5f1e476f9ec8f0455c564a9b69e4fca455d3d201aef8500fef7f24455b278772152233d5a36a1db7de9afad4ed37'


class StagingError(ValueError):
    """Operator-safe error text containing no private input values."""


def networks(value):
    try:
        result = [ipaddress.ip_network(item, strict=True) for item in value.split(',')]
        if not result or any(item.prefixlen == 0 or item.is_multicast or item.is_unspecified for item in result):
            raise ValueError()
        return result
    except ValueError:
        raise StagingError('ACL must contain explicit canonical client networks; unrestricted access is refused') from None


def stage(args, recursion, management):
    output = args.output
    if not output.is_absolute() or output.exists() or output.is_symlink():
        raise StagingError('output must be a new absolute private directory')
    for parent in (output.parent, *output.parent.parents):
        if (parent / '.git').exists() or parent.is_symlink():
            raise StagingError('output must be outside Git and symlinked directories')
    output.mkdir(mode=0o700)
    try:
        for archive, folder in ((args.app_archive, 'app'), (args.runtime_archive, 'runtime')):
            target = output / folder
            target.mkdir()
            with tarfile.open(archive, 'r:gz') as stream:
                stream.extractall(target, filter='data')
        if not (output / 'app/DnsServerApp.dll').is_file() or not (output / 'runtime/dotnet').is_file():
            raise StagingError('verified archives lack native entrypoints')
        source = Path(__file__).resolve().parent
        for name in ('install.sh', 'remove.sh', 'technitium-pi.service', 'technitium-pi-firewall.service'):
            shutil.copyfile(source / name, output / name)
        settings = {
            'DNS_SERVER_DOMAIN': 'raspberrypi.home',
            'DNS_SERVER_ADMIN_PASSWORD_FILE': '/etc/technitium-pi/admin-password',
            'DNS_SERVER_WEB_SERVICE_LOCAL_ADDRESSES': '127.0.0.1',
            'DNS_SERVER_WEB_SERVICE_HTTP_PORT': '5380',
            'DNS_SERVER_RECURSION': 'UseSpecifiedNetworkACL',
            'DNS_SERVER_RECURSION_NETWORK_ACL': ','.join(map(str, recursion)),
            'DNS_SERVER_ENABLE_BLOCKING': 'true',
            'DNS_SERVER_LOG_MAX_LOG_FILE_DAYS': '7',
            'DNS_SERVER_STATS_ENABLE_IN_MEMORY_STATS': 'true',
        }
        (output / 'bootstrap.env').write_text(''.join(f'{key}={value}\n' for key, value in settings.items()))
        rules = ['add table inet technitium_pi', 'flush table inet technitium_pi',
                 'table inet technitium_pi {', ' chain input {',
                 '  type filter hook input priority -10; policy accept;',
                 '  iifname "lo" accept']
        for allowed, ports in ((recursion, ('udp dport 53', 'tcp dport 53')), (management, ('tcp dport 53443',))):
            for version, family in ((4, 'ip'), (6, 'ip6')):
                members = ', '.join(str(item) for item in allowed if item.version == version)
                if members:
                    for port in ports:
                        rules.append(f'  {family} saddr {{ {members} }} {port} accept')
        rules += ['  udp dport { 53, 67 } drop', '  tcp dport { 53, 5380, 53443 } drop',
                  ' }', ' chain output {', '  type filter hook output priority -10; policy accept;',
                  '  udp sport 67 drop', ' }', '}']
        (output / 'firewall.nft').write_text('\n'.join(rules) + '\n')
        for file in output.iterdir():
            if file.is_file():
                file.chmod(0o600)
    except Exception:
        shutil.rmtree(output)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['stage'])
    parser.add_argument('--app-archive', type=Path, required=True)
    parser.add_argument('--runtime-archive', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--recursion-acl', required=True)
    parser.add_argument('--management-acl', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        recursion = networks(args.recursion_acl)
        management = networks(args.management_acl)
        for path, algorithm, expected in ((args.app_archive, 'sha256', APP_SHA256),
                                           (args.runtime_archive, 'sha512', RUNTIME_SHA512)):
            with path.open('rb') as stream:
                if hashlib.file_digest(stream, algorithm).hexdigest() != expected:
                    raise StagingError('archive checksum mismatch')
        stage(args, recursion, management)
    except StagingError as error:
        print(f'native: {error}', file=sys.stderr)
        return 64
    except (OSError, ValueError, tarfile.TarError):
        print('native: staging refused; check ACL, archive checksum, private output path and archive contents', file=sys.stderr)
        return 64
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
