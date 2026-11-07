#!/usr/bin/env python3
"""Observe the Site DNS Cluster without changing either Host."""
import argparse
from datetime import datetime, timezone
import json
import os
import subprocess
import sys

import yaml

from dns import (Api, CLUSTER_CATALOG, CLUSTER_DOMAIN, VERSION, cluster_matches,
                 cluster_members, cluster_ptr_records, decode_json, load_declaration,
                 matching, normalize, private_path)

# Pinned v15.6.2 settings/get fields that affect this DNS-only deployment.
POLICY_FIELDS = (
    'enableBlocking', 'allowTxtBlockingReport', 'blockingType', 'blockingAnswerTtl',
    'blockingBypassList', 'customBlockingAddresses', 'blockListUrls',
    'blockListUpdateIntervalHours', 'recursion', 'recursionNetworkACL',
    'dnssecValidation', 'minSoaRefresh', 'minSoaRetry', 'forwarders', 'proxy',
)


def instant(value):
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.tzinfo is None:
        raise ValueError('timestamp requires timezone')
    return parsed


def observe(api, zones):
    return {
        'cluster': api.call('admin/cluster/state'),
        'settings': api.call('settings/get'),
        'scopes': api.call('dhcp/scopes/list')['scopes'],
        'allowed': api.call('allowed/export', export=True),
        'blocked': api.call('blocked/export', export=True),
        'zones': {zone['name']: zone for zone in api.call('zones/list')['zones']},
        'records': {zone: api.call('zones/records/get', {
            'zone': zone, 'domain': zone, 'listZone': 'true'})['records'] for zone in [*zones, CLUSTER_DOMAIN]},
        'options': {zone: api.call('zones/options/get', {'zone': zone}) for zone in zones},
    }


def health(desired, peers):
    now = datetime.now(timezone.utc)
    issues = []
    zones = []
    expected = cluster_members(desired)
    for role, peer in peers.items():
        cluster = peer['cluster']
        nodes = cluster['clusterNodes']
        local_name = desired['members'][role]['host'] + '.' + CLUSTER_DOMAIN
        if (not cluster_matches(cluster, expected)
                or cluster['dnsServerDomain'] != local_name
                or any(node['state'] != ('Self' if node['name'] == local_name else 'Connected')
                       for node in nodes)):
            issues.append(role + ':membership')
        for domain, kind in (
                (CLUSTER_DOMAIN, 'Primary' if role == 'primary' else 'Secondary'),
                (CLUSTER_CATALOG, 'Catalog' if role == 'primary' else 'SecondaryCatalog')):
            zone = peer['zones'].get(domain)
            if (zone is None or zone['type'] != kind or zone['disabled']
                    or (role == 'secondary' and (zone['isExpired'] or zone['syncFailed']))):
                issues.append(role + ':cluster-zones')
        if not peer['settings']['enableBlocking']:
            issues.append(role + ':filtering-disabled')
        if peer['settings']['version'] != VERSION:
            issues.append(role + ':version')
        # Neither Host should depend on its peer as an upstream resolver.
        if (peer['settings']['forwarders'] or peer['settings']['proxy']
                or peer['settings']['recursion'] == 'Deny'):
            issues.append(role + ':independent-recursion')
        if any(scope['enabled'] for scope in peer['scopes']):
            issues.append(role + ':dhcp-enabled')
    primary, secondary = peers['primary'], peers['secondary']
    for key in POLICY_FIELDS:
        if primary['settings'][key] != secondary['settings'][key]:
            issues.append('policy:' + key)
    for key in ('allowed', 'blocked'):
        if primary[key] != secondary[key]:
            issues.append('policy:' + key)
    secondary_self = next(node for node in secondary['cluster']['clusterNodes']
                          if node['state'] == 'Self' and node['type'] == 'Secondary')
    age = (now - instant(secondary_self['configLastSynced'])).total_seconds()
    if not 0 <= age <= 1800:
        issues.append('secondary:config-age')
    member_ptrs = {}
    for role, peer in peers.items():
        try:
            member_ptrs[role] = cluster_ptr_records(desired, peer['records'][CLUSTER_DOMAIN])
        except (ValueError, KeyError):
            member_ptrs[role] = {}
            issues.append(role + ':cluster-addresses')
    for index, zone in enumerate(desired['zones']):
        label = 'home' if index == 0 else 'reverse'
        for role, peer in peers.items():
            summary = peer['zones'][zone]
            options = peer['options'][zone]
            if (summary['type'] != role.capitalize() or summary['disabled']
                    or options['catalog'] != CLUSTER_CATALOG
                    or (role == 'primary' and options['update'] != 'Deny')):
                issues.append(role + ':' + label + ':zone-policy')
            for record in desired['records']:
                record = member_ptrs[role].get(record['id'], record)
                if record['zone'] != zone or record['type'] in ('NS', 'SOA'):
                    continue
                current = [item for item in peer['records'][zone]
                           if item['name'] == record['name'] and item['type'] == record['type']]
                if not matching(current, record):
                    kind = 'cluster-ptrs' if record['id'] in member_ptrs[role] else 'declared-records'
                    issues.append(role + ':' + label + ':' + kind)
        left = sorted(json.dumps(normalize(item), sort_keys=True) for item in primary['records'][zone])
        right = sorted(json.dumps(normalize(item), sort_keys=True) for item in secondary['records'][zone])
        if left != right or primary['zones'][zone]['soaSerial'] != secondary['zones'][zone]['soaSerial']:
            issues.append(label + ':divergence')
        records = secondary['records'][zone]
        soa = next(item for item in records if item['type'] == 'SOA')['rData']
        ns = {item['rData']['nameServer'] for item in records if item['type'] == 'NS'}
        if (ns != set(expected) or soa['primaryNameServer'] != desired['members']['primary']['host'] + '.' + CLUSTER_DOMAIN
                or any(soa[key] != value for key, value in next(
                    item for item in desired['records'] if item['zone'] == zone and item['type'] == 'SOA'
                )['values'][0].items() if key in ('refresh', 'retry', 'expire', 'minimum'))):
            issues.append(label + ':authority')
        remaining = (instant(secondary['zones'][zone]['expiry']) - now).total_seconds()
        refresh_age = soa['expire'] - remaining
        if (secondary['zones'][zone]['isExpired'] or secondary['zones'][zone]['syncFailed']
                or remaining <= 259200 or refresh_age < -60):
            issues.append(label + ':expiry-margin')
        zones.append({'id': label, 'serial': secondary['zones'][zone]['soaSerial'],
                      'remainingSeconds': int(remaining), 'refreshAgeSeconds': max(0, int(refresh_age))})
    return {'status': 'degraded' if issues else 'healthy', 'issues': sorted(set(issues)),
            'configAgeSeconds': int(age), 'zones': zones}


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site', required=True)
    parser.add_argument('--primary-connection', required=True)
    parser.add_argument('--secondary-connection', required=True)
    parser.add_argument('--isolated', action='store_true')
    args = parser.parse_args()
    try:
        desired = load_declaration(args.site)
        apis = {}
        for role in ('primary', 'secondary'):
            connection = decode_json(private_path(getattr(args, role + '_connection'), True).read_text())
            if connection['instance'] != desired['members'][role]['instance']:
                raise ValueError('connection does not match Site membership')
            apis[role] = Api(connection, args.isolated)
        if apis['primary'].base == apis['secondary'].base:
            raise ValueError('require two independent endpoints')
        result = health(desired, {role: observe(api, desired['zones']) for role, api in apis.items()})
    except (OSError, ValueError, KeyError, TypeError, AttributeError, StopIteration, yaml.YAMLError,
            subprocess.SubprocessError):
        result = {'status': 'unavailable', 'issues': ['observation-failed']}
    print(json.dumps(result, sort_keys=True))
    return 0 if result['status'] == 'healthy' else 1


if __name__ == '__main__':
    sys.exit(main())
