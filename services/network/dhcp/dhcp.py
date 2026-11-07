#!/usr/bin/env python3
"""Stage protected Site DHCP configuration with allocation disabled."""
import argparse
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import time
import zipfile

NETWORK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(NETWORK / 'dns'))
sys.path.insert(0, str(NETWORK / 'backup'))
import dns
import backup
import yaml

SCOPE = 'home'


def declaration(site):
    desired = dns.load_declaration(site)
    site = Path(desired['site'])
    spec = yaml.load((site / 'site.yaml').read_text(), Loader=dns.InventoryLoader)['spec']
    document = site / spec['sensitiveValues']['document']
    entries = dns.decode_json(dns.run(['sops', 'decrypt', '--output-type', 'json', document]))['spec']['values']
    values = {entry['id']: entry['value'] for entry in entries}
    if len(values) != len(entries):
        raise ValueError('duplicate sensitive reference')
    network = spec['network']
    dhcp = network['dhcp']
    if dhcp['owner'] != desired['primary']:
        raise ValueError('DHCP owner must be the primary')
    subnet = ipaddress.IPv4Network(values[network['subnet']])

    def address(reference):
        result = ipaddress.IPv4Address(values[reference])
        if result not in subnet or result in (subnet.network_address, subnet.broadcast_address):
            raise ValueError('address outside usable subnet')
        return result

    rs, re_, ds, de = [address(dhcp[key]) for key in
                       ('reservationStart', 'reservationEnd', 'dynamicStart', 'dynamicEnd')]
    if not rs <= re_ < ds <= de:
        raise ValueError('invalid allocation bands')
    # A single scope spans both bands: all space below the dynamic band is excluded.
    # This also excludes any gap between the declared reservation and dynamic bands.
    fixed = [address(network['gateway'])]
    for reference in spec['hosts']:
        host = yaml.load((site / reference).read_text(), Loader=dns.InventoryLoader)['spec']
        if host.get('networkAddress'):
            fixed.append(address(host['networkAddress']))
    if len(set(fixed)) != len(fixed) or any(rs <= value <= de for value in fixed):
        raise ValueError('static address collision')
    reservations = []
    names, macs, addresses = set(), set(), set(fixed)
    reserved_names = {record['name'] for record in desired['records']}
    for entry in dhcp['reservations']:
        label = dns.name(entry['name'])
        raw_mac = values[entry['mac']].replace(':', '-').upper()
        if not re.fullmatch(r'(?:[0-9A-F]{2}-){5}[0-9A-F]{2}', raw_mac) or int(raw_mac[:2], 16) & 1:
            raise ValueError('invalid unicast MAC')
        ip = address(entry['address'])
        if (label in names or raw_mac in macs or ip in addresses or not rs <= ip <= re_
                or '.' in label or label + '.home' in reserved_names or label == 'cluster'):
            raise ValueError('reservation collision')
        names.add(label)
        macs.add(raw_mac)
        addresses.add(ip)
        reservations.append({'hostName': label, 'hardwareAddress': raw_mac,
                             'address': str(ip), 'comments': 'Site reservation: ' + label})
    scope = {'name': SCOPE, 'startingAddress': str(rs), 'endingAddress': str(de),
             'subnetMask': str(subnet.netmask), 'routerAddress': str(fixed[0]),
             'dnsServers': [desired['members'][role]['resolver'] for role in ('primary', 'secondary')],
             'useThisDnsServer': False, 'domainName': 'home', 'domainSearchList': ['home'],
             'dnsUpdates': True, 'dnsOverwriteForDynamicLease': False,
             'leaseTimeDays': 1, 'leaseTimeHours': 0, 'leaseTimeMinutes': 0,
             'allowOnlyReservedLeases': False, 'blockLocallyAdministeredMacAddresses': False,
             'ignoreClientIdentifierOption': True,
             'exclusions': [{'startingAddress': str(rs), 'endingAddress': str(ds - 1)}],
             'reservedLeases': sorted(reservations, key=lambda item: item['hostName'])}
    desired['scope'] = scope
    # Reject changes between the two decryptions/reads, rather than mixing snapshots.
    if dns.load_declaration(site) != {key: value for key, value in desired.items() if key != 'scope'}:
        raise ValueError('declarations changed while loading')
    return desired


def observe(api):
    scopes = []
    for summary in api.call('dhcp/scopes/list')['scopes']:
        detail = api.call('dhcp/scopes/get', {'name': summary['name']})
        scopes.append(dict(detail, enabled=summary['enabled']))
    return {'version': api.call('settings/get')['version'],
            'cluster': api.call('admin/cluster/state'),
            'scopes': sorted(scopes, key=lambda scope: scope['name']),
            'leases': api.call('dhcp/leases/list')['leases']}


def canonical(scope, fields):
    result = {key: scope.get(key) for key in fields}
    result['reservedLeases'] = sorted(result.get('reservedLeases') or [], key=lambda item: item['hostName'])
    return result


def context(args, state):
    desired = declaration(args.site)
    connection = dns.decode_json(dns.private_path(args.connection, True).read_text())
    if connection['instance'] != desired['primary']:
        raise ValueError('only primary may stage DHCP')
    api = dns.Api(connection, args.isolated)
    previous = dns.decode_json(state.read_text()) if state.exists() else {}
    binding = dns.state_binding(desired, connection)
    if previous and previous['binding'] != binding:
        raise ValueError('journal binding mismatch')
    live = observe(api)
    return desired, api, previous, live


def plan_for(desired, api, previous, live):
    wanted = desired['scope']
    current = next((scope for scope in live['scopes'] if scope['name'] == SCOPE), None)
    changes, blockers, unknown = [], [], []
    if live['version'] != dns.VERSION:
        blockers.append('DHCP_VERSION_MISMATCH')
    cluster = live['cluster']
    if cluster['clusterInitialized']:
        if (not dns.cluster_matches(cluster, dns.cluster_members(desired)) or not any(
                node['type'] == 'Primary' and node['state'] == 'Self' for node in cluster['clusterNodes'])):
            blockers.append('DHCP_ENDPOINT_NOT_DECLARED_PRIMARY')
    if any(scope['enabled'] for scope in live['scopes']):
        blockers.append('DHCP_ALLOCATION_MUST_BE_DISABLED')
    if live['leases']:
        blockers.append('DHCP_RETAINED_LEASES_REQUIRE_HANDOFF')
    start, end = (ipaddress.IPv4Address(wanted[key]) for key in ('startingAddress', 'endingAddress'))
    for scope in live['scopes']:
        if scope['name'] == SCOPE:
            continue
        unknown.append({'id': api.fingerprint(scope['name'])[:16], 'kind': 'scope'})
        if (ipaddress.IPv4Address(scope['startingAddress']) <= end
                and ipaddress.IPv4Address(scope['endingAddress']) >= start):
            blockers.append('DHCP_UNKNOWN_SCOPE_OVERLAP')
    if current is None:
        changes.append({'id': 'scope:home', 'action': 'create'})
    else:
        old = {item['hostName']: item for item in previous.get('scope', {}).get('reservedLeases', [])}
        new = {item['hostName']: item for item in wanted['reservedLeases']}
        fields = set(wanted) - {'reservedLeases'}
        if any(current.get(key) != wanted[key] for key in fields):
            changes.append({'id': 'scope:home', 'action': 'update-options'})
        existing = {item['hostName']: item for item in current['reservedLeases']}
        if len(existing) != len(current['reservedLeases']):
            blockers.append('DHCP_DUPLICATE_LIVE_RESERVATION')
        for label, reservation in new.items():
            if existing.get(label) != reservation:
                changes.append({'id': 'reservation:' + label, 'action': 'update' if label in existing else 'add'})
        for label, reservation in existing.items():
            if label not in new:
                if label in old:
                    changes.append({'id': 'reservation:' + label, 'action': 'delete'})
                else:
                    unknown.append({'id': api.fingerprint(reservation)[:16], 'kind': 'reservation'})
                    blockers.append('DHCP_UNKNOWN_RESERVATIONS_REQUIRE_REVIEW')
        # Replacing a scope must never silently discard unowned options.
        extra = ('serverAddress', 'serverHostName', 'bootFileName', 'winsServers', 'ntpServers',
                 'ntpServerDomainNames', 'staticRoutes', 'vendorInfo', 'capwapAcIpAddresses',
                 'tftpServerAddresses', 'genericOptions')
        defaults = {'offerDelayTime': 0, 'pingCheckEnabled': False,
                    'pingCheckTimeout': 1000, 'pingCheckRetries': 2, 'dnsTtl': 900}
        if (any(current.get(key) for key in extra)
                or any(current.get(key, value) != value for key, value in defaults.items())):
            blockers.append('DHCP_UNOWNED_OPTIONS_REQUIRE_REVIEW')
    commit = dns.run(['git', '-C', dns.ROOT, 'rev-parse', 'HEAD']).decode().strip()
    implementation = {str(path.relative_to(NETWORK)): hashlib.sha256(path.read_bytes()).hexdigest()
                      for path in (Path(__file__), NETWORK / 'dns/dns.py', NETWORK / 'backup/backup.py',
                                   NETWORK / 'technitium.env')}
    identifier = api.fingerprint({'desired': desired, 'previous': previous, 'live': live,
        'connection': api.connection, 'commit': commit, 'implementation': implementation})
    return {'plan': identifier, 'commit': commit, 'protected': bool(changes),
            'changes': changes, 'unknown': unknown, 'blockers': sorted(set(blockers))}


def scope_values(scope):
    values = dict(scope)
    values['dnsServers'] = ','.join(scope['dnsServers'])
    values['domainSearchList'] = ','.join(scope['domainSearchList'])
    values['exclusions'] = '|'.join(value for pair in scope['exclusions'] for value in
                                  (pair['startingAddress'], pair['endingAddress']))
    values['reservedLeases'] = '|'.join(value for lease in scope['reservedLeases'] for value in
        (lease['hostName'], lease['hardwareAddress'], lease['address'], lease['comments']))
    return {key: str(value).lower() if type(value) is bool else value for key, value in values.items()}


def import_scope(api, archive):
    boundary = 'dhcp-' + secrets.token_hex(16)
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="scope.zip"\r\n'
            'Content-Type: application/zip\r\n\r\n').encode() + archive + f'\r\n--{boundary}--\r\n'.encode()
    result = json.loads(api.request('settings/restore?scopes=true', body,
                                   'multipart/form-data; boundary=' + boundary))
    if result.get('status') != 'ok':
        raise ValueError('disabled scope import failed')


def stage_scope(scope):
    """The set API is permitted only inside this fresh, private internal bridge."""
    identifier = 'technitium-dhcp-' + secrets.token_hex(10)
    network, container = identifier + '-net', identifier
    created_network = created_container = False
    password = secrets.token_hex(32)
    try:
        backup.command(['docker', 'network', 'create', '--internal', network])
        created_network = True
        with tempfile.TemporaryDirectory(prefix='dhcp-stage-') as workspace:
            environment = Path(workspace) / 'environment'
            backup.write_private(environment, ('DNS_SERVER_ADMIN_PASSWORD=' + password + '\n'
                                              'DNS_SERVER_DOMAIN=stage.pilot.test\n').encode())
            backup.command(['docker', 'create', '--name', container, '--network', network,
                '--mount', 'type=volume,destination=/etc/dns', '--env-file', environment, backup.IMAGE])
        created_container = True
        backup.command(['docker', 'start', container])
        api = backup.isolated_api(container, network, password)
        if api.call('settings/get')['version'] != dns.VERSION:
            raise ValueError('staging version mismatch')
        api.call('dhcp/scopes/set', scope_values(scope))
        api.call('dhcp/scopes/disable', {'name': SCOPE})
        if any(item['enabled'] for item in api.call('dhcp/scopes/list')['scopes']):
            raise ValueError('staging scope not disabled')
        if canonical(api.call('dhcp/scopes/get', {'name': SCOPE}), scope) != scope:
            raise ValueError('staged scope differs from declaration')
        # Scope saves are asynchronous. Export only the selected scope after it is flushed.
        for attempt in range(10):
            files = backup.zip_files(api.request('settings/backup?scopes=true'))
            selected = {key: value for key, value in files.items() if key == 'scopes/home.scope'}
            if selected:
                break
            time.sleep(1)
        if len(selected) != 1:
            raise ValueError('staged scope export missing')
        archive = backup.make_zip(backup.disabled_scope_files(selected))
        import_scope(api, archive)
        backup.command(['docker', 'restart', container])
        api = backup.isolated_api(container, network, password)
        if (any(item['enabled'] for item in api.call('dhcp/scopes/list')['scopes'])
                or canonical(api.call('dhcp/scopes/get', {'name': SCOPE}), scope) != scope
                or api.call('dhcp/leases/list')['leases']):
            raise ValueError('disabled scope failed reload verification')
        return archive
    finally:
        try:
            if created_container:
                backup.command(['docker', 'rm', '-f', '-v', container])
        finally:
            if created_network:
                backup.command(['docker', 'network', 'rm', network])


def save_state(state, desired, api):
    dns.save_private_json(state, {'binding': dns.state_binding(desired, api.connection),
                                 'scope': desired['scope']})


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('read', 'plan', 'apply', 'verify'))
    for flag in ('site', 'connection', 'state'):
        parser.add_argument('--' + flag, required=True)
    for flag in ('plan', 'backup-archive', 'backup-identity'):
        parser.add_argument('--' + flag)
    parser.add_argument('--isolated', action='store_true', help='loopback HTTP fixtures only')
    args = parser.parse_args()
    try:
        with dns.locked_state(args.state) as state:
            desired, api, previous, live = context(args, state)
            plan = plan_for(desired, api, previous, live)
            if args.command in ('apply', 'verify'):
                if plan['blockers']:
                    raise ValueError('plan blocked')
                if args.command == 'verify' and plan['changes']:
                    raise ValueError('configuration differs')
                if args.command == 'apply':
                    if args.plan is not None and not hmac.compare_digest(args.plan, plan['plan']):
                        raise ValueError('stale plan')
                    if plan['changes']:
                        if not args.plan or not args.backup_archive or not args.backup_identity:
                            raise ValueError('exact approval and fresh paired backup required')
                        dns.run([sys.executable, '-B', NETWORK / 'backup/backup.py', 'gate',
                            '--archive', args.backup_archive, '--identity', args.backup_identity, '--max-age-hours', '24'])
                        archive = stage_scope(desired['scope'])
                        refreshed = context(args, state)
                        if plan_for(*refreshed)['plan'] != plan['plan']:
                            raise ValueError('inputs changed during staging')
                        desired, api, previous, live = refreshed
                        transport = backup.Api(api.connection['url'], api.token,
                            api.connection.get('caFile'), isolated=args.isolated)
                        import_scope(transport, archive)
                        verified = plan_for(desired, api, previous, observe(api))
                        if verified['changes'] or verified['blockers']:
                            raise ValueError('post-import verification failed')
                    save_state(state, desired, api)
                result = {'status': 'verified', 'allocation': 'disabled', 'reservations': len(desired['scope']['reservedLeases'])}
            else:
                result = plan
            print(json.dumps(result, sort_keys=True))
        return 0
    except (OSError, ValueError, KeyError, TypeError, AttributeError, yaml.YAMLError,
            subprocess.SubprocessError, zipfile.BadZipFile):
        print('DHCP operation failed; check private inputs, isolation, backup and exact current plan.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
