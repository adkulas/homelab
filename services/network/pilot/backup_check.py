#!/usr/bin/env python3
"""Exercise the operator backup interface against both pinned disposable DNS nodes."""
import io
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import zipfile

from automation_check import Api
from pilot_support import COMPOSE, inspect_pilot, output

CLI = Path('services/network/backup/backup.py').resolve()


def run(*args):
    return subprocess.run(list(map(str, args)), capture_output=True, check=True).stdout


def cli(*args):
    return json.loads(run(sys.executable, '-B', CLI, *args))


def main():
    workspace = Path(sys.argv[1])
    password = Path(os.environ['PILOT_PASSWORD_FILE']).read_text().strip()
    addresses, network = inspect_pilot()
    apis = {name: Api(address, password) for name, address in addresses.items()}
    primary, secondary = apis['primary'], apis['secondary']
    primary.call('admin/cluster/init', {'clusterDomain': 'cluster.pilot.test',
                                      'primaryNodeIpAddresses': addresses['primary']})
    secondary.call('admin/cluster/initJoin', {
        'secondaryNodeIpAddresses': addresses['secondary'],
        'primaryNodeUrl': 'https://primary.cluster.pilot.test:53443/',
        'primaryNodeIpAddress': addresses['primary'], 'ignoreCertificateErrors': 'true',
        'primaryNodeUsername': 'admin', 'primaryNodePassword': password})
    # Joining replaces the secondary authentication configuration.
    apis['secondary'] = Api(addresses['secondary'], password)
    subnet = ipaddress.ip_network(network['IPAM']['Config'][0]['Subnet'])
    address = lambda offset: str(subnet.network_address + offset)
    primary.call('zones/create', {'zone': 'backup.pilot.test', 'type': 'Primary'})
    primary.call('zones/records/add', {'domain': 'service.backup.pilot.test', 'type': 'A',
                                     'ipAddress': address(30)})
    primary.call('dhcp/scopes/set', {
        'name': 'backup-pilot', 'startingAddress': address(20), 'endingAddress': address(120),
        'subnetMask': str(subnet.netmask), 'routerAddress': network['IPAM']['Config'][0]['Gateway'],
        'dnsServers': ','.join(addresses.values()), 'useThisDnsServer': 'false', 'domainName': 'home',
        'reservedLeases': f'backup-client|02-56-00-00-00-30|{address(30)}|backup'})
    # This scope is deliberately enabled on the disposable source bridge. Recovery must disable it.
    if not primary.call('dhcp/scopes/list')['scopes'][0]['enabled']:
        raise RuntimeError('source scope must exercise enabled-scope recovery')
    settings = {'nodes': {}, 'recipients': []}
    for service, api in apis.items():
        cert, key = workspace / (service + '.crt'), workspace / (service + '.key')
        pfx = workspace / (service + '.pfx')
        run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key,
            '-out', cert, '-days', '1', '-subj', '/CN=backup-pilot', '-addext',
            'subjectAltName=IP:' + addresses[service])
        run('openssl', 'pkcs12', '-export', '-out', pfx, '-inkey', key, '-in', cert,
            '-passout', 'pass:')
        container = output(*COMPOSE, 'ps', '-q', service)
        run('docker', 'cp', pfx, container + ':/etc/dns/backup-pilot.pfx')
        api.call('settings/set', {'webServiceEnableTls': 'true',
                                'webServiceUseSelfSignedTlsCertificate': 'false',
                                'webServiceTlsCertificatePath': '/etc/dns/backup-pilot.pfx',
                                'webServiceTlsCertificatePassword': ''})
        token = workspace / (service + '.token')
        token.write_text(api.token)
        token.chmod(0o600)
        node = 'homelab' if service == 'primary' else 'raspberrypi'
        settings['nodes'][node] = {'url': f'https://{addresses[service]}:53443',
                                  'caFile': str(cert), 'tokenFile': str(token)}
    identity = workspace / 'recovery.key'
    run('age-keygen', '-o', identity)
    recipient = run('age-keygen', '-y', identity).decode().strip()
    settings['recipients'] = [recipient]
    config = workspace / 'backup.json'
    config.write_text(json.dumps(settings))
    config.chmod(0o600)
    archive = workspace / 'pair.age'
    # Technitium flushes settings and zone files asynchronously. Capture must refuse incomplete files.
    deadline = time.monotonic() + 60
    while True:
        result = subprocess.run([sys.executable, '-B', str(CLI), 'create', '--config', str(config),
                                 '--output', str(archive), '--identity', str(identity)], capture_output=True)
        if result.returncode == 0:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError('paired backup never passed completeness and HTTPS checks')
        time.sleep(2)
    print('paired encrypted backup: both nodes, trusted TLS, cluster/auth/settings/zones/scopes')
    pc_copy = workspace / 'pc.age'
    pc_copy.write_bytes(archive.read_bytes())
    passport_copy = workspace / 'passport.age'
    passport_copy.write_bytes(archive.read_bytes())
    for copy in (pc_copy, passport_copy):
        cli('verify', '--archive', copy, '--identity', identity)
    cli('gate', '--archive', pc_copy, '--identity', identity, '--max-age-hours', '24')
    print('PC and Passport copy verification: decryption and per-file integrity passed')
    payload = run('age', '-d', '-i', identity, archive)
    with zipfile.ZipFile(io.BytesIO(payload)) as bundle:
        source = bundle.read('homelab.zip')
    with zipfile.ZipFile(io.BytesIO(source)) as node:
        source_files = {name: node.read(name) for name in node.namelist()}
    for node, mode in (('homelab', 'drill'), ('homelab', 'live'), ('raspberrypi', 'live')):
        plan = workspace / (node + '-' + mode + '-plan.json')
        target = workspace / (node + '-' + mode)
        cli('restore-plan', '--archive', archive, '--identity', identity, '--node', node,
            '--mode', mode, '--output', target, '--plan', plan)
        if target.exists() or cli('restore', '--plan', plan, '--identity', identity)['status'] != 'confirmation-required':
            raise RuntimeError('restore dry run unexpectedly mutated state')
        report = cli('restore', '--plan', plan, '--identity', identity, '--confirm')
        if report['status'] != 'recovered-stopped' or report['dhcp'] != 'disabled':
            raise RuntimeError('restore did not leave stopped, DHCP-disabled configuration')
        if node == 'homelab':
            if (target / 'zones/backup.pilot.test.zone').read_bytes() != source_files['zones/backup.pilot.test.zone']:
                raise RuntimeError('recovered zone differs from backup')
            before = source_files['scopes/backup-pilot.scope']
            after = (target / 'scopes/backup-pilot.scope').read_bytes()
            # Independent literal from the fixture's v10 header: 12-byte name, then enabled.
            if (not before.startswith(b'SC\x0a\x0cbackup-pilot\x01')
                    or not after.startswith(b'SC\x0a\x0cbackup-pilot\x00')
                    or before[17:] != after[17:]):
                raise RuntimeError('scope recovery changed reservation/options/lease bytes or retained enabled state')
            if mode == 'live':
                for name in ('auth.config', 'cluster.config', 'dns.config', 'webservice.config'):
                    if (target / name).read_bytes() != source_files[name]:
                        raise RuntimeError('live recovery lost original node configuration')
        print(f'{node} {mode}: exact-plan recovery, zone data checked, DHCP disabled across restart; output stopped')
    print('backup rehearsal passed; no household services were changed')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError, zipfile.BadZipFile):
        print('backup rehearsal failed; private diagnostics and inputs remain in the runner workspace until teardown', file=sys.stderr)
        sys.exit(1)
