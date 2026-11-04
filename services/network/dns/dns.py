#!/usr/bin/env python3
"""Reconcile Git-owned local DNS on the primary DNS Stack Instance."""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request

import yaml

ROOT = Path(__file__).resolve().parents[3]
VERSION = (ROOT / 'services/network/technitium.env').read_text().split('@')[0].rsplit(':', 1)[1]
FIELDS = {'A': 'ipAddress', 'CNAME': 'cname', 'PTR': 'ptrName', 'NS': 'nameServer'}


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate document field')
        result[key] = value
    return result


class InventoryLoader(yaml.SafeLoader):
    pass


def unique_yaml(loader, node):
    return unique_fields(loader.construct_pairs(node, deep=True))



InventoryLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, unique_yaml)


def decode_json(data):
    return json.loads(data, object_pairs_hook=unique_fields)


def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def run(args):
    result = subprocess.run(list(map(str, args)), capture_output=True, timeout=45)
    if result.returncode:
        raise ValueError('external operation failed')
    return result.stdout


def private_path(value, existing=False):
    path = Path(value).absolute()
    if (path != path.resolve() or not path.parent.is_dir()
            or any((parent / '.git').exists() for parent in (path, *path.parents))):
        raise ValueError('private paths must be outside Git without symlinks')
    if path.exists() or existing:
        if not path.is_file() or path.stat().st_mode & 0o077:
            raise ValueError('private files require mode 0600')
    return path


@contextmanager
def locked_state(value):
    path = private_path(value)
    descriptor = os.open(str(path) + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield path


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError('API redirects are forbidden')


class Api:
    def __init__(self, connection, isolated):
        self.connection = connection
        url = urllib.parse.urlsplit(connection['url'])
        if (url.scheme != ('http' if isolated else 'https') or not url.hostname
                or url.username or url.password or url.query or url.fragment
                or url.path not in ('', '/')
                or (isolated and url.hostname not in ('127.0.0.1', '::1'))):
            raise ValueError('require HTTPS origin, or loopback-only isolated HTTP')
        self.base = connection['url'].rstrip('/') + '/api/'
        self.token = private_path(connection['tokenFile'], True).read_text().strip()
        if not self.token:
            raise ValueError('empty token')
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect(),
            urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=connection.get('caFile'))))

    def call(self, path, values=None):
        request = urllib.request.Request(self.base + path,
            data=urllib.parse.urlencode(values or {}).encode(),
            headers={'Authorization': 'Bearer ' + self.token,
                     'Content-Type': 'application/x-www-form-urlencoded'})
        with self.opener.open(request, timeout=30) as reply:
            body = reply.read(4 * 1024 * 1024 + 1)
        if len(body) > 4 * 1024 * 1024:
            raise ValueError('API response exceeds limit')
        result = json.loads(body)
        if result.get('status') != 'ok':
            raise ValueError('API rejected operation')
        return result.get('response', result)

    def fingerprint(self, value):
        return hmac.new(self.token.encode(), encode(value), hashlib.sha256).hexdigest()


def name(value):
    if (not isinstance(value, str) or value != value.lower() or len(value) > 253
            or any(not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', label)
                   for label in value.split('.'))):
        raise ValueError('invalid DNS name')
    return value


def load_declaration(site):
    site = Path(site).resolve()
    sources = {}

    def read(reference, json_document=False):
        path = (site / reference).resolve()
        if not path.is_relative_to(site):
            raise ValueError('Site reference escapes inventory')
        data = path.read_bytes()
        sources[reference] = hashlib.sha256(data).hexdigest()
        return decode_json(data) if json_document else yaml.load(data, Loader=InventoryLoader)

    inventory = read('site.yaml')
    spec = inventory['spec']
    policy = read('network.json', True)
    if (inventory['apiVersion'] != 'homelab.site/v1alpha1' or inventory['kind'] != 'Site'
            or inventory['metadata']['name'] != 'home'
            or set(policy) != {'apiVersion', 'kind', 'spec'}
            or policy['apiVersion'] != 'homelab.network/v1alpha1' or policy['kind'] != 'SiteDNS'):
        raise ValueError('unsupported Site or DNS policy')
    policy = policy['spec']
    if set(policy) != {'domain', 'ttl', 'soa', 'aliases'} or policy['domain'] != 'home':
        raise ValueError('unsupported DNS policy fields')
    ttl = policy['ttl']
    if type(ttl) is not int or not 30 <= ttl <= 86400:
        raise ValueError('invalid TTL')
    soa = policy['soa']
    if (set(soa) != {'refresh', 'retry', 'expire', 'minimum'}
            or any(type(value) is not int or value < 1 or value > 2147483647 for value in soa.values())
            or soa['expire'] <= max(259200, soa['refresh'], soa['retry'])):
        raise ValueError('SOA expiry must exceed the 72-hour margin')
    hosts = {}
    for reference in spec['hosts']:
        host = read(reference)
        identity = name(host['metadata']['name'])
        if identity in hosts or host['kind'] != 'Host' or host['apiVersion'] != inventory['apiVersion']:
            raise ValueError('invalid Host declaration')
        hosts[identity] = host['spec']
    instances = {}
    for reference in spec['stackInstances']:
        instance = read(reference)
        identity = instance['metadata']['name']
        if identity in instances:
            raise ValueError('duplicate Stack Instance')
        if instance['kind'] != 'StackInstance' or instance['apiVersion'] != inventory['apiVersion']:
            raise ValueError('invalid Stack Instance declaration')
        instances[identity] = instance['spec']
    primary_id = spec['dnsCluster']['primary']
    secondary_id = spec['dnsCluster']['secondary']
    if primary_id == secondary_id:
        raise ValueError('duplicate DNS Cluster member')
    for identity, role in ((primary_id, 'primary'), (secondary_id, 'secondary')):
        instance = instances[identity]
        if (instance['stack'] != 'network' or instance['network']['role'] != role
                or instance['host'] not in hosts or not hosts[instance['host']].get('networkAddress')):
            raise ValueError('invalid DNS Cluster member')
    primary = instances[primary_id]['host']
    if primary == instances[secondary_id]['host']:
        raise ValueError('DNS Cluster members require distinct Hosts')
    document = spec['sensitiveValues']['document']
    read(document)
    values_document = decode_json(run(['sops', 'decrypt', '--output-type', 'json', site / document]))
    if (values_document['apiVersion'] != 'homelab.site/v1alpha1'
            or values_document['kind'] != 'SiteSensitiveValues'):
        raise ValueError('unsupported sensitive document')
    entries = values_document['spec']['values']
    values = {entry['id']: entry['value'] for entry in entries}
    if len(values) != len(entries):
        raise ValueError('duplicate sensitive reference')
    subnet = ipaddress.IPv4Network(values[spec['network']['subnet']])
    if subnet.prefixlen not in (8, 16, 24):
        raise ValueError('reverse DNS requires an octet-aligned /8, /16 or /24 subnet')
    reverse = '.'.join(reversed(str(subnet.network_address).split('.')[:subnet.prefixlen // 8])) + '.in-addr.arpa'
    records = []

    def record(identity, zone, domain, kind, data):
        records.append({'id': identity, 'zone': zone, 'name': domain, 'type': kind,
                        'ttl': ttl, 'values': data})

    addresses = {}
    for host, settings in hosts.items():
        if not settings.get('networkAddress'):
            continue
        address = ipaddress.IPv4Address(values[settings['networkAddress']])
        if (address not in subnet or address in (subnet.network_address, subnet.broadcast_address)
                or str(address) in addresses.values()):
            raise ValueError('invalid or duplicate Host address')
        addresses[host] = str(address)
        record('host:' + host, 'home', host + '.home', 'A', [{'ipAddress': str(address)}])
        record('ptr:' + host, reverse, address.reverse_pointer, 'PTR', [{'ptrName': host + '.home'}])
    for alias, target in policy['aliases'].items():
        name(alias)
        if (not alias.endswith('.home') or alias == 'cluster.home' or alias.endswith('.cluster.home')
                or alias in (host + '.home' for host in hosts) or target not in addresses):
            raise ValueError('alias must reference an addressed Host and avoid reserved names')
        record('alias:' + alias, 'home', alias, 'CNAME', [{'cname': target + '.home'}])
    for zone, label in (('home', 'home'), (reverse, 'reverse')):
        record('ns:' + label, zone, zone, 'NS', [{'nameServer': primary + '.home'}])
        record('soa:' + label, zone, zone, 'SOA', [dict(soa,
            primaryNameServer=primary + '.home', responsiblePerson='hostmaster.home')])
    return {'site': str(site), 'zones': ['home', reverse], 'records': records, 'primary': primary_id,
            'resolver': addresses[primary], 'sources': sources}


def normalize(record):
    data = {key: value for key, value in record['rData'].items()
            if not key.endswith(('String', 'Idn')) and key != 'useSerialDateScheme'}
    if record['type'] == 'SOA':
        data.pop('serial', None)
    for key in ('cname', 'ptrName', 'nameServer', 'primaryNameServer', 'responsiblePerson'):
        if key in data:
            data[key] = data[key].lower().rstrip('.')
    contact = data.get('responsiblePerson', '')
    if '@' in contact:
        mailbox, domain = contact.rsplit('@', 1)
        data['responsiblePerson'] = mailbox.replace('\\', '\\\\').replace('.', '\\.') + '.' + domain
    return {'name': record['name'].lower().rstrip('.'), 'type': record['type'],
            'ttl': record['ttl'], 'disabled': record.get('disabled', False),
            'expiryTtl': record.get('expiryTtl', 0), 'data': data}


def observe(api, desired, previous):
    listing = api.call('zones/list')['zones']
    wanted = set(desired['zones']) | set(previous.get('zones', []))
    # Cluster records remain Technitium-owned but invalidate an outstanding approval.
    wanted.update(zone['name'] for zone in listing
                  if zone['name'] == 'cluster.home' or zone['name'].endswith('.cluster.home'))
    live = {}
    for zone in listing:
        if zone['name'] not in wanted:
            continue
        label = zone['name']
        options = api.call('zones/options/get', {'zone': label})
        records = api.call('zones/records/get', {'zone': label, 'domain': label, 'listZone': 'true'})['records']
        live[label] = {'type': zone['type'], 'disabled': zone['disabled'],
                       'options': options, 'records': records}
    scopes = []
    for summary in api.call('dhcp/scopes/list')['scopes']:
        detail = api.call('dhcp/scopes/get', {'name': summary['name']})
        scopes.append(dict(detail, enabled=summary['enabled']))
    return {'zones': live, 'scopes': scopes,
            'leases': api.call('dhcp/leases/list')['leases'],
            'version': api.call('settings/get')['version'],
            'cluster': api.call('admin/cluster/state')}


def record_identity(record, zone=None):
    return (record['zone'] if zone is None else zone,
            record['name'].lower().rstrip('.'), record['type'])


def current_records(live, record):
    return [item for item in live['zones'].get(record['zone'], {}).get('records', [])
            if record_identity(item, record['zone']) == record_identity(record)]


def matching(current, desired):
    expected = [{'name': desired['name'], 'type': desired['type'], 'ttl': desired['ttl'],
                 'disabled': False, 'expiryTtl': 0, 'data': value} for value in desired['values']]
    return sorted(map(encode, map(normalize, current))) == sorted(map(encode, expected))


def configuration(value, field=None):
    if isinstance(value, dict):
        return {key: configuration(item, key) for key, item in value.items()
                if key not in ('lastUsedOn', 'lastModified', 'lastSeen', 'configLastSynced',
                               'notifyFailed', 'notifyFailedFor')
                and not key.endswith(('String', 'Idn'))}
    if isinstance(value, list):
        items = [configuration(item) for item in value]
        return sorted(items, key=encode) if field in ('records', 'clusterNodes', 'scopes', 'leases') else items
    return value


def make_plan(api, desired, previous, live):
    changes = []
    blockers = []
    if live['version'] != VERSION:
        blockers.append('DNS_VERSION_MISMATCH')
    cluster = live['cluster']
    if cluster['clusterInitialized'] and not any(
            member['type'] == 'Primary' and member['state'] == 'Self'
            for member in cluster['clusterNodes']):
        blockers.append('DNS_ENDPOINT_NOT_PRIMARY')
    known = {record_identity(item) for item in desired['records']}
    unknown = []
    for zone in desired['zones']:
        current = live['zones'].get(zone)
        label = 'zone:home' if zone == 'home' else 'zone:reverse'
        if current is None:
            changes.append({'id': label, 'action': 'create-zone', 'protected': True})
        elif current['type'] != 'Primary':
            blockers.append('DNS_ZONE_NOT_PRIMARY')
        elif current['disabled'] or current['options'].get('disabled', False) or current['options'].get('update') != 'Deny':
            changes.append({'id': label, 'action': 'secure-zone', 'protected': True})
    for scope in live['scopes']:
        retained_leases = any(lease['scope'] == scope['name'] for lease in live['leases'])
        if scope.get('dnsUpdates', False) and (scope['enabled'] or retained_leases):
            blockers.append('DHCP_DNS_UPDATES_REQUIRE_COLLISION_PROTECTION')
    previous_keys = {record_identity(item) for item in previous.get('records', [])}
    for record in desired['records']:
        for other in live['zones'].get(record['zone'], {}).get('records', []):
            if (other['name'].lower().rstrip('.') == record['name']
                    and other['type'] != record['type']
                    and 'CNAME' in (other['type'], record['type'])):
                blockers.append('DNS_NAME_COLLISION')
        current = current_records(live, record)
        if matching(current, record):
            continue
        protected = not record['id'].startswith('alias:')
        # Claiming an existing manual record is never a routine alias update.
        key = record_identity(record)
        if current and key not in previous_keys:
            protected = True
        changes.append({'id': record['id'], 'action': 'update' if current else 'add',
                        'protected': protected, 'before': '<redacted>' if current else None,
                        'after': '<redacted>', 'ttl': record['ttl'],
                        'valueChanged': sorted(encode(normalize(item)['data']) for item in current)
                                        != sorted(map(encode, record['values']))})
    for record in previous.get('records', []):
        key = record_identity(record)
        if key not in known and record['zone'] in desired['zones'] and current_records(live, record):
            changes.append({'id': record['id'], 'action': 'delete', 'protected': True,
                            'before': '<redacted>', 'after': None})
    for zone in previous.get('zones', []):
        if zone in desired['zones'] or zone not in live['zones']:
            continue
        current = live['zones'][zone]
        if current['type'] != 'Primary':
            blockers.append('DNS_ZONE_NOT_PRIMARY')
        for record in current['records']:
            key = record_identity(record, zone)
            if key not in previous_keys:
                blockers.append('RETIRED_ZONE_CONTAINS_UNKNOWN_RECORDS')
        changes.append({'id': 'zone:retired:' + api.fingerprint(zone)[:16],
                        'action': 'delete-zone', 'protected': True})
    for zone, current in live['zones'].items():
        for record in current['records']:
            key = record_identity(record, zone)
            if key not in known and key not in previous_keys:
                unknown.append({'id': api.fingerprint(key)[:16], 'type': record['type'],
                                'value': '<redacted>'})
    commit = run(['git', '-C', ROOT, 'rev-parse', 'HEAD']).decode().strip()
    fingerprint = api.fingerprint({'commit': commit, 'desired': desired, 'previous': previous,
                                   'live': configuration(live), 'connection': api.connection,
                                   'implementation': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()})
    return {'plan': fingerprint, 'commit': commit, 'protected': any(item['protected'] for item in changes),
            'changes': changes, 'unknown': unknown, 'blockers': sorted(set(blockers))}


def verify(api, desired, previous):
    plan = make_plan(api, desired, previous, observe(api, desired, previous))
    if plan['changes'] or plan['blockers']:
        raise ValueError('post-apply DNS differs from declaration')
    checked = 0
    for record in desired['records']:
        if record['type'] not in ('A', 'CNAME', 'PTR'):
            continue
        answer = run(['dig', '@' + desired['resolver'], record['name'], record['type'],
                      '+short', '+time=2', '+tries=1']).decode().strip().lower()
        expected = sorted(value[FIELDS[record['type']]] for value in record['values'])
        if sorted(line.rstrip('.') for line in answer.splitlines()) != expected:
            raise ValueError('declared DNS query failed')
        checked += 1
    return {'status': 'verified', 'queries': checked, 'unknown': plan['unknown']}


def state_binding(desired, connection):
    return {'site': desired['site'], 'instance': desired['primary'],
            'url': connection['url'].rstrip('/')}


def save_state(path, desired, connection):
    descriptor, temporary = tempfile.mkstemp(prefix='.dns-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'wb') as output:
            output.write(encode({'binding': state_binding(desired, connection),
                                 'zones': desired['zones'], 'records': desired['records']}))
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def apply(api, desired, previous, live, plan, args, state):
    approval = args.plan
    if plan['blockers']:
        raise ValueError('plan contains blockers')
    if (approval is not None and not hmac.compare_digest(approval, plan['plan'])) or (plan['protected'] and approval is None):
        raise ValueError('apply the exact current protected plan')
    if plan['protected']:
        if not args.backup_archive or not args.backup_identity:
            raise ValueError('protected changes require verified fresh backup')
        run([sys.executable, '-B', ROOT / 'services/network/backup/backup.py', 'gate',
             '--archive', args.backup_archive, '--identity', args.backup_identity, '--max-age-hours', '24'])
        # Revalidate every input after backup verification, before the first mutation.
        desired, api, previous = load_context(args, state)
        live = observe(api, desired, previous)
        refreshed = make_plan(api, desired, previous, live)
        if not hmac.compare_digest(refreshed['plan'], plan['plan']):
            raise ValueError('plan inputs changed while checking backup')
    records = {record['id']: record for record in desired['records']}
    old_records = {record['id']: record for record in previous.get('records', [])}
    for change in plan['changes']:
        action = change['action']
        if action in ('create-zone', 'secure-zone'):
            zone = desired['zones'][0 if change['id'] == 'zone:home' else 1]
            if action == 'create-zone':
                api.call('zones/create', {'zone': zone, 'type': 'Primary'})
            api.call('zones/options/set', {'zone': zone, 'update': 'Deny', 'disabled': 'false'})
            continue
        if action == 'delete-zone':
            zone = next(zone for zone in previous['zones']
                        if change['id'] == 'zone:retired:' + api.fingerprint(zone)[:16])
            api.call('zones/delete', {'zone': zone})
            continue
        record = old_records[change['id']] if action == 'delete' else records[change['id']]
        values = {'zone': record['zone'], 'domain': record['name'], 'type': record['type']}
        if action == 'delete':
            for current in current_records(live, record):
                data = current['rData']
                api.call('zones/records/delete', dict(values, **data))
            continue
        for index, data in enumerate(record['values']):
            values = dict(values, **data, ttl=record['ttl'], overwrite='true' if index == 0 else 'false')
            if record['type'] == 'SOA':
                current = api.call('zones/records/get', {'zone': record['zone'],
                    'domain': record['zone'], 'listZone': 'true'})['records']
                soa = next(item for item in current if item['type'] == 'SOA')
                values.update(serial=soa['rData']['serial'],
                              useSerialDateScheme=str(soa['rData'].get('useSerialDateScheme', False)).lower())
                api.call('zones/records/update', values)
            else:
                api.call('zones/records/add', values)
    return verify(api, desired, previous)


def load_context(args, state):
    desired = load_declaration(args.site)
    connection = decode_json(private_path(args.connection, True).read_text())
    if connection['instance'] != desired['primary']:
        raise ValueError('only the primary DNS Stack Instance may reconcile')
    api = Api(connection, args.isolated)
    previous = decode_json(state.read_text()) if state.exists() else {}
    if state.exists() and previous.get('binding') != state_binding(desired, connection):
        raise ValueError('ownership journal belongs to another Site or endpoint')
    return desired, api, previous


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('read', 'plan', 'apply', 'verify'))
    parser.add_argument('--site', required=True)
    parser.add_argument('--connection', required=True)
    parser.add_argument('--state', required=True)
    parser.add_argument('--plan')
    parser.add_argument('--backup-archive')
    parser.add_argument('--backup-identity')
    parser.add_argument('--isolated', action='store_true', help='loopback HTTP fixtures only')
    args = parser.parse_args()
    try:
        with locked_state(args.state) as state:
            desired, api, previous = load_context(args, state)
            live = observe(api, desired, previous)
            plan = make_plan(api, desired, previous, live)
            if args.command == 'apply':
                result = apply(api, desired, previous, live, plan, args, state)
                save_state(state, desired, api.connection)
            elif args.command == 'verify':
                result = verify(api, desired, previous)
            else:
                result = plan
            print(json.dumps(result, sort_keys=True))
        return 0
    except (OSError, ValueError, KeyError, TypeError, AttributeError, yaml.YAMLError, subprocess.SubprocessError):
        print('DNS operation failed; check private inputs, API access and the exact current plan.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
