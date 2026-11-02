"""Exercise the operator CLI against disposable HTTP and process boundaries."""
import copy
from datetime import datetime, timedelta, timezone
import hashlib
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import shutil
import tempfile
import threading
import unittest
import zipfile
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[3]
CLI = ROOT / 'services/network/dns/dns.py'


class DNSTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.site = self.root / 'site'
        (self.site / 'hosts').mkdir(parents=True)
        (self.site / 'instances').mkdir()
        (self.site / 'secrets').mkdir()
        self.zones = {}
        self.scopes = []
        self.writes = []
        self.fail_write = False
        self.bad_answers = False
        self.version = '15.6.2'
        self.commit = 'a' * 40
        self.cluster = {'clusterInitialized': False, 'dnsServerDomain': 'homelab.home'}
        test = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                if self.headers.get('Authorization') != 'Bearer fixture-token':
                    self.send_error(403)
                    return
                values = {key: entries[0] for key, entries in parse_qs(
                    self.rfile.read(int(self.headers['Content-Length'])).decode(),
                    keep_blank_values=True).items()}
                path = self.path.removeprefix('/api/')
                try:
                    result = test.api(path, values)
                    reply = {'status': 'ok', 'response': result}
                except (ValueError, KeyError):
                    reply = {'status': 'error', 'errorMessage': 'private 192.0.2.10 fixture-token'}
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(reply).encode())

            def do_GET(self):
                query = parse_qs(urlsplit(self.path).query)
                answers = []
                if not test.bad_answers:
                    for zone in test.zones.values():
                        for record in zone['records']:
                            if record['name'] == query['name'][0] and record['type'] == query['type'][0]:
                                answers.append(next(iter(record['rData'].values())))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps(answers).encode())

        self.handler = Handler
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.url = f'http://127.0.0.1:{self.server.server_port}'
        self.connection = self.root / 'connection.json'
        self.token = self.root / 'token'
        self.token.write_text('fixture-token')
        self.token.chmod(0o600)
        self.connection.write_text(json.dumps({'url': self.url, 'tokenFile': str(self.token),
                                               'instance': 'homelab-dns'}))
        self.connection.chmod(0o600)
        self.state = self.root / 'ownership.json'
        self.identity = self.root / 'recovery.key'
        key = subprocess.run(['age-keygen', '-o', str(self.identity)], capture_output=True, check=True)
        self.recipient = key.stderr.decode().split('Public key: ')[1].strip()
        self.backup = self.root / 'paired.age'
        self.with_backup = True
        self.write_backup()
        self.policy = {'apiVersion': 'homelab.network/v1alpha1', 'kind': 'SiteDNS', 'spec': {
            'domain': 'home', 'ttl': 300,
            'soa': {'refresh': 900, 'retry': 300, 'expire': 604800, 'minimum': 300},
            'aliases': {'jellyfin.home': 'homelab'}}}
        self.write_policy()
        (self.site / 'site.yaml').write_text('''apiVersion: homelab.site/v1alpha1
kind: Site
metadata: {name: home}
spec:
  hosts: [hosts/homelab.yaml, hosts/raspberrypi.yaml]
  stackInstances: [instances/homelab-dns.yaml, instances/raspberrypi-dns.yaml]
  dnsCluster: {primary: homelab-dns, secondary: raspberrypi-dns}
  sensitiveValues: {document: secrets/site.sops.yaml}
  network: {subnet: lan-subnet}
''')
        for host in ('homelab', 'raspberrypi'):
            (self.site / 'hosts' / (host + '.yaml')).write_text(
                f'apiVersion: homelab.site/v1alpha1\nkind: Host\nmetadata: {{name: {host}}}\n'
                f'spec: {{networkAddress: {host}-address}}\n')
            role = 'primary' if host == 'homelab' else 'secondary'
            (self.site / 'instances' / (host + '-dns.yaml')).write_text(
                f'apiVersion: homelab.site/v1alpha1\nkind: StackInstance\nmetadata: {{name: {host}-dns}}\n'
                f'spec: {{stack: network, host: {host}, network: {{role: {role}}}}}\n')
        self.encrypted = self.site / 'secrets/site.sops.yaml'
        self.encrypted.write_text('encrypted fixture')
        self.values = {'apiVersion': 'homelab.site/v1alpha1', 'kind': 'SiteSensitiveValues',
                       'spec': {'values': [
            {'id': 'lan-subnet', 'value': '192.0.2.0/24'},
            {'id': 'homelab-address', 'value': '192.0.2.10'},
            {'id': 'raspberrypi-address', 'value': '192.0.2.11'}]}}
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        sops = self.bin / 'sops'
        sops.write_text('#!/usr/bin/env python3\nimport os\nprint(os.environ["FIXTURE_VALUES"])\n')
        sops.chmod(0o755)
        git = self.bin / 'git'
        git.write_text('#!/usr/bin/env python3\nimport os\nprint(os.environ["FIXTURE_COMMIT"])\n')
        git.chmod(0o755)
        dig = self.bin / 'dig'
        dig.write_text('''#!/usr/bin/env python3
import json, os, sys, urllib.parse, urllib.request
query = urllib.parse.urlencode({'name': sys.argv[2], 'type': sys.argv[3]})
with urllib.request.urlopen(os.environ['FIXTURE_URL'] + '/resolve?' + query) as reply:
    for value in json.load(reply):
        print(value)
''')
        dig.chmod(0o755)

    def write_backup(self, created=None):
        def zip_bytes(files):
            output = io.BytesIO()
            with zipfile.ZipFile(output, 'w') as archive:
                for filename, data in files.items():
                    archive.writestr(filename, data)
            return output.getvalue()
        files = {'auth.config': b'auth', 'dns.config': b'dns', 'webservice.config': b'web'}
        payload = zip_bytes(files)
        nodes = {node: {'version': '15.6.2', 'sha256': hashlib.sha256(payload).hexdigest(),
                 'files': {key: hashlib.sha256(value).hexdigest() for key, value in files.items()},
                 'observation': {'clusterInitialized': False, 'scopes': 0, 'zones': 0}}
                 for node in ('homelab', 'raspberrypi')}
        manifest = {'format': 1, 'nodes': nodes,
                    'created': (created or datetime.now(timezone.utc)).isoformat()}
        contents = zip_bytes({'manifest.json': json.dumps(manifest),
                              'homelab.zip': payload, 'raspberrypi.zip': payload})
        encrypted = subprocess.run(['age', '-r', self.recipient], input=contents,
                                   capture_output=True, check=True).stdout
        self.backup.write_bytes(encrypted)

    def write_policy(self):
        (self.site / 'network.json').write_text(json.dumps(self.policy))

    def cli(self, command, *extra, success=True):
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                   FIXTURE_VALUES=json.dumps(self.values), FIXTURE_URL=self.url, FIXTURE_COMMIT=self.commit,
                   FIXTURE_GATE_TARGET=str(getattr(self, 'gate_target', '')),
                   FIXTURE_REAL_AGE=shutil.which('age', path=os.environ['PATH']))
        if command == 'apply' and self.with_backup:
            extra = (*extra, '--backup-archive', str(self.backup), '--backup-identity', str(self.identity))
        result = subprocess.run(['python3', '-B', str(CLI), command, '--site', str(self.site),
                                 '--connection', str(self.connection), '--state', str(self.state),
                                 '--isolated', *extra], cwd=ROOT, env=env, text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
        for secret in ('192.0.2.', '2.0.192.in-addr', 'fixture-token'):
            self.assertNotIn(secret, result.stdout + result.stderr)
        return json.loads(result.stdout) if success else result

    def api(self, path, values):
        if path == 'settings/get':
            return {'version': self.version}
        if path == 'admin/cluster/state':
            return copy.deepcopy(self.cluster)
        if path == 'zones/list':
            return {'zones': [{'name': name, 'type': 'Primary', 'disabled': False}
                              for name in self.zones]}
        if path == 'dhcp/scopes/list':
            return {'scopes': [{'name': scope['name'], 'enabled': scope.get('enabled', False)}
                               for scope in self.scopes]}
        if path == 'dhcp/scopes/get':
            # Pinned API exposes enabled in list, not in the scope detail response.
            scope = next(scope for scope in self.scopes if scope['name'] == values['name'])
            return {key: value for key, value in scope.items() if key != 'enabled'}
        zone = values.get('zone')
        if path == 'zones/options/get':
            return self.zones[zone]['options']
        if path == 'zones/records/get':
            if values.get('listZone') != 'true':
                raise ValueError('must read whole zone')
            return {'records': copy.deepcopy(self.zones[zone]['records'])}
        self.writes.append((path, values))
        if self.fail_write:
            raise ValueError('fixture rejection')
        if path == 'zones/create':
            self.zones[zone] = {'options': {'update': 'Deny', 'disabled': False}, 'records': [
                {'name': zone, 'type': 'SOA', 'ttl': 900, 'disabled': False, 'rData': {
                    'primaryNameServer': 'initial.home', 'responsiblePerson': 'hostmaster.home',
                    'serial': 1, 'refresh': 900, 'retry': 300, 'expire': 604800, 'minimum': 900}},
                {'name': zone, 'type': 'NS', 'ttl': 900, 'disabled': False,
                 'rData': {'nameServer': 'initial.home'}}]}
        elif path == 'zones/delete':
            del self.zones[zone]
        elif path == 'zones/options/set':
            self.zones[zone]['options'].update(values)
            if 'disabled' in values:
                self.zones[zone]['options']['disabled'] = values['disabled'] == 'true'
        elif path in ('zones/records/add', 'zones/records/update'):
            kind = values['type']
            fields = {'A': ('ipAddress',), 'CNAME': ('cname',), 'PTR': ('ptrName',),
                      'NS': ('nameServer',), 'SOA': ('primaryNameServer', 'responsiblePerson',
                          'serial', 'refresh', 'retry', 'expire', 'minimum')}[kind]
            records = self.zones[zone]['records']
            if path.endswith('update') or values.get('overwrite') == 'true':
                records[:] = [record for record in records if
                              (record['name'], record['type']) != (values['domain'], kind)]
            data = {field: int(values[field]) if field in
                    ('serial', 'refresh', 'retry', 'expire', 'minimum') else values[field] for field in fields}
            records.append({'name': values['domain'], 'type': kind, 'ttl': int(values['ttl']),
                            'disabled': False, 'rData': data})
        elif path == 'zones/records/delete':
            records = self.zones[zone]['records']
            records[:] = [record for record in records if
                          (record['name'], record['type']) != (values['domain'], values['type'])]
        else:
            raise ValueError('unsupported endpoint')
        return {}

    def test_initial_plan_is_read_only_redacted_and_requires_exact_approval(self):
        plan = self.cli('plan')
        self.assertTrue(plan['protected'])
        self.assertIn('host:homelab', [change['id'] for change in plan['changes']])
        self.assertEqual(self.writes, [])
        self.cli('apply', success=False)
        self.assertEqual(self.writes, [])
        self.cli('apply', '--plan', 'wrong', success=False)
        self.assertEqual(self.writes, [])

    def test_exact_apply_verifies_dns_and_repeated_apply_is_empty(self):
        plan = self.cli('plan')
        result = self.cli('apply', '--plan', plan['plan'])
        self.assertEqual(result['status'], 'verified')
        self.assertEqual(self.cli('plan')['changes'], [])
        self.assertEqual(self.cli('verify')['status'], 'verified')
        count = len(self.writes)
        self.cli('apply')
        self.assertEqual(len(self.writes), count)
        self.assertEqual(self.state.stat().st_mode & 0o777, 0o600)

    def bootstrap(self):
        plan = self.cli('plan')
        self.cli('apply', '--plan', plan['plan'])

    def test_reverse_zone_change_requires_exact_plan_and_removes_only_owned_zone(self):
        self.bootstrap()
        self.values['spec']['values'][0]['value'] = '198.51.100.0/24'
        self.values['spec']['values'][1]['value'] = '198.51.100.10'
        self.values['spec']['values'][2]['value'] = '198.51.100.11'
        plan = self.cli('plan')
        self.assertIn('delete-zone', [change['action'] for change in plan['changes']])
        count = len(self.writes)
        self.cli('apply', success=False)
        self.assertEqual(len(self.writes), count)
        self.cli('apply', '--plan', plan['plan'])
        self.assertEqual(self.cli('plan')['changes'], [])
        self.assertNotIn('2.0.192.in-addr.arpa', self.zones)

    def test_unknown_record_at_new_alias_blocks_the_whole_apply(self):
        self.bootstrap()
        self.zones['home']['records'].append({'name': 'sonarr.home', 'type': 'A', 'ttl': 300,
            'disabled': False, 'rData': {'ipAddress': '192.0.2.99'}})
        self.policy['spec']['aliases']['sonarr.home'] = 'homelab'
        self.write_policy()
        plan = self.cli('plan')
        self.assertIn('DNS_NAME_COLLISION', plan['blockers'])
        self.assertEqual(len(plan['unknown']), 1)
        count = len(self.writes)
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(len(self.writes), count)

    def test_vendor_presentation_fields_and_query_timestamps_do_not_create_drift(self):
        self.bootstrap()
        for zone in self.zones.values():
            for record in zone['records']:
                record['lastUsedOn'] = '2026-01-01T00:00:00'
                record['expiryTtl'] = 0
                if record['type'] == 'SOA':
                    record['rData'].update(refreshString='15m', retryString='5m',
                        expireString='7d', minimumString='5m', useSerialDateScheme=False)
        first = self.cli('plan')
        self.assertEqual(first['changes'], [])
        for zone in self.zones.values():
            for record in zone['records']:
                record['lastUsedOn'] = '2026-01-02T00:00:00'
        self.assertEqual(self.cli('plan')['plan'], first['plan'])

    def test_wrong_version_and_secondary_api_endpoints_block_all_mutations(self):
        self.version = '16.0'
        plan = self.cli('plan')
        self.assertIn('DNS_VERSION_MISMATCH', plan['blockers'])
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])
        self.version = '15.6.2'
        self.cluster = {'clusterInitialized': True, 'dnsServerDomain': 'raspberrypi.cluster.home',
                        'clusterNodes': [{'name': 'raspberrypi.cluster.home', 'type': 'Secondary', 'state': 'Self'}]}
        plan = self.cli('plan')
        self.assertIn('DNS_ENDPOINT_NOT_PRIMARY', plan['blockers'])
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])

    def test_ownership_state_cannot_be_reused_for_another_api_endpoint(self):
        self.bootstrap()
        server = ThreadingHTTPServer(('127.0.0.1', 0), self.handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        connection = json.loads(self.connection.read_text())
        connection['url'] = f'http://127.0.0.1:{server.server_port}'
        self.connection.write_text(json.dumps(connection))
        self.cli('plan', success=False)

    def test_duplicate_policy_fields_are_rejected_before_any_write(self):
        policy = self.site / 'network.json'
        policy.write_text(policy.read_text().replace('"ttl": 300', '"ttl": 30, "ttl": 300'))
        self.cli('apply', success=False)
        self.assertEqual(self.writes, [])
        self.cli('plan', success=False)

    def test_alias_add_update_and_delete_preserve_unknown_records(self):
        self.bootstrap()
        unknown = {'name': 'manual.home', 'type': 'A', 'ttl': 60, 'disabled': False,
                   'rData': {'ipAddress': '192.0.2.90'}}
        self.zones['home']['records'].append(unknown)
        self.policy['spec']['aliases']['sonarr.home'] = 'raspberrypi'
        self.policy['spec']['aliases']['jellyfin.home'] = 'raspberrypi'
        self.write_policy()
        plan = self.cli('read')
        self.assertFalse(plan['protected'])
        self.assertEqual({change['id'] for change in plan['changes']},
                         {'alias:sonarr.home', 'alias:jellyfin.home'})
        self.assertEqual(len(plan['unknown']), 1)
        self.cli('apply')
        self.assertIn(unknown, self.zones['home']['records'])
        del self.policy['spec']['aliases']['sonarr.home']
        self.write_policy()
        plan = self.cli('plan')
        self.assertEqual([(item['id'], item['action']) for item in plan['changes']],
                         [('alias:sonarr.home', 'delete')])
        count = len(self.writes)
        self.cli('apply', success=False)
        self.assertEqual(len(self.writes), count)
        self.cli('apply', '--plan', plan['plan'])
        self.assertIn(unknown, self.zones['home']['records'])
        self.assertEqual(self.cli('plan')['changes'], [])

    def test_fixed_record_changes_and_removals_are_precise_and_protected(self):
        site_file = self.site / 'site.yaml'
        site_file.write_text(site_file.read_text().replace(
            'hosts/raspberrypi.yaml]', 'hosts/raspberrypi.yaml, hosts/pc.yaml]'))
        host_file = self.site / 'hosts/pc.yaml'
        host_file.write_text('apiVersion: homelab.site/v1alpha1\nkind: Host\n'
                             'metadata: {name: pc}\nspec: {networkAddress: pc-address}\n')
        self.values['spec']['values'].append({'id': 'pc-address', 'value': '192.0.2.42'})
        self.bootstrap()
        self.values['spec']['values'][-1]['value'] = '192.0.2.43'
        plan = self.cli('plan')
        self.assertTrue(plan['protected'])
        self.assertEqual([(item['id'], item['action']) for item in plan['changes']],
                         [('host:pc', 'update'), ('ptr:pc', 'add'), ('ptr:pc', 'delete')])
        self.cli('apply', '--plan', plan['plan'])
        host_file.write_text(host_file.read_text().replace('{networkAddress: pc-address}', '{}'))
        plan = self.cli('plan')
        self.assertEqual([(item['id'], item['action']) for item in plan['changes']],
                         [('host:pc', 'delete'), ('ptr:pc', 'delete')])
        self.cli('apply', '--plan', plan['plan'])
        self.assertEqual(self.cli('plan')['changes'], [])

    def test_stale_plans_reject_changed_source_decrypted_values_live_state_and_cluster(self):
        self.bootstrap()
        self.policy['spec']['aliases']['jellyfin.home'] = 'raspberrypi'
        self.write_policy()
        source = self.site / 'network.json'
        previous_source = source.read_text()
        changes = [
            lambda: source.write_text(previous_source + '\n'),
            lambda: self.values['spec']['values'][1].update(value='192.0.2.12'),
            lambda: self.zones['home']['records'].append({'name': 'new.home', 'type': 'A',
                     'ttl': 60, 'rData': {'ipAddress': '192.0.2.99'}}),
            lambda: self.cluster.update(clusterDomain='cluster.home'),
            lambda: self.token.write_text('rotated-token'),
        ]
        for change in changes:
            with self.subTest(change=changes.index(change)):
                original_zones = copy.deepcopy(self.zones)
                original_values = copy.deepcopy(self.values)
                original_cluster = copy.deepcopy(self.cluster)
                plan = self.cli('plan')
                count = len(self.writes)
                change()
                self.cli('apply', '--plan', plan['plan'], success=False)
                self.assertEqual(len(self.writes), count)
                source.write_text(previous_source)
                self.zones = original_zones
                self.values = original_values
                self.cluster = original_cluster
                self.token.write_text('fixture-token')

    def test_ns_soa_host_reverse_and_zone_security_drift_are_all_protected(self):
        self.bootstrap()
        cases = [('home', 'NS'), ('home', 'SOA'), ('home', 'A'), ('2.0.192.in-addr.arpa', 'PTR')]
        for zone, kind in cases:
            with self.subTest(zone=zone, kind=kind):
                record = next(record for record in self.zones[zone]['records'] if record['type'] == kind)
                record['ttl'] = 3600
                plan = self.cli('plan')
                self.assertTrue(plan['protected'])
                count = len(self.writes)
                self.cli('apply', success=False)
                self.assertEqual(len(self.writes), count)
                self.cli('apply', '--plan', plan['plan'])
        self.zones['home']['options']['update'] = 'Allow'
        plan = self.cli('plan')
        self.assertTrue(plan['protected'])
        self.cli('apply', '--plan', plan['plan'])
        self.assertEqual(self.zones['home']['options']['update'], 'Deny')

    def test_dhcp_dns_updates_block_even_when_dynamic_overwrite_is_false(self):
        self.bootstrap()
        self.scopes = [{'name': 'household', 'domainName': 'home', 'enabled': True,
                        'dnsUpdates': True, 'dnsOverwriteForDynamicLease': False,
                        'reservedLeases': [{'hostName': 'homelab'}]}]
        self.policy['spec']['aliases']['sonarr.home'] = 'homelab'
        self.write_policy()
        plan = self.cli('plan')
        self.assertIn('DHCP_DNS_UPDATES_REQUIRE_COLLISION_PROTECTION', plan['blockers'])
        count = len(self.writes)
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.cli('verify', success=False)
        self.assertEqual(len(self.writes), count)

    def test_unknown_records_prevent_retired_zone_deletion(self):
        self.bootstrap()
        record = {'name': '90.2.0.192.in-addr.arpa', 'type': 'PTR', 'ttl': 300,
                  'rData': {'ptrName': 'manual.home'}}
        self.zones['2.0.192.in-addr.arpa']['records'].append(record)
        for entry, value in zip(self.values['spec']['values'],
                                ('198.51.100.0/24', '198.51.100.10', '198.51.100.11')):
            entry['value'] = value
        plan = self.cli('plan')
        self.assertIn('RETIRED_ZONE_CONTAINS_UNKNOWN_RECORDS', plan['blockers'])
        count = len(self.writes)
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(len(self.writes), count)
        self.assertIn(record, self.zones['2.0.192.in-addr.arpa']['records'])

    def test_api_and_dns_failures_never_publish_successful_ownership_state(self):
        plan = self.cli('plan')
        self.fail_write = True
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertFalse(self.state.exists())
        self.fail_write = False
        self.bad_answers = True
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertFalse(self.state.exists())
        self.cli('verify', success=False)
        self.bad_answers = False
        self.cli('apply')
        self.assertEqual(self.cli('verify')['status'], 'verified')

    def test_invalid_declarations_are_rejected_without_writes(self):
        original = copy.deepcopy(self.policy)
        cases = [
            lambda: self.policy['spec'].update(unsupported=True),
            lambda: self.policy['spec']['aliases'].update({'cluster.home': 'homelab'}),
            lambda: self.policy['spec']['aliases'].update({'homelab.home': 'raspberrypi'}),
            lambda: self.policy['spec']['aliases'].update({'bad.home': 'missing-host'}),
            lambda: self.policy['spec']['soa'].update(expire=259200),
        ]
        for change in cases:
            change()
            self.write_policy()
            self.cli('plan', success=False)
            self.assertEqual(self.writes, [])
            self.policy = copy.deepcopy(original)
        self.write_policy()
        for invalid in ('192.0.2.10', '198.51.100.11'):
            self.values['spec']['values'][2]['value'] = invalid
            self.cli('plan', success=False)
            self.assertEqual(self.writes, [])

    def test_protected_apply_requires_a_fresh_decryptable_verified_backup(self):
        plan = self.cli('plan')
        self.with_backup = False
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])
        self.with_backup = True
        self.write_backup(datetime.now(timezone.utc) - timedelta(hours=25))
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])
        self.backup.write_bytes(b'corrupt archive')
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])
        self.write_backup()
        self.cli('apply', '--plan', plan['plan'])
        self.policy['spec']['aliases']['sonarr.home'] = 'homelab'
        self.write_policy()
        self.with_backup = False
        self.cli('apply')

    def test_changed_commit_invalidates_plan_and_acl_order_is_significant(self):
        self.bootstrap()
        self.policy['spec']['aliases']['jellyfin.home'] = 'raspberrypi'
        self.write_policy()
        first = self.cli('plan')
        self.commit = 'b' * 40
        count = len(self.writes)
        self.cli('apply', '--plan', first['plan'], success=False)
        self.assertEqual(len(self.writes), count)
        self.zones['home']['options']['queryAccessNetworkACL'] = ['!192.0.2.0/25', '192.0.2.0/24']
        first = self.cli('plan')
        self.zones['home']['options']['queryAccessNetworkACL'].reverse()
        self.cli('apply', '--plan', first['plan'], success=False)
        self.assertEqual(len(self.writes), count)

    def test_undecryptable_or_wrong_sensitive_document_fails_before_writes(self):
        self.values['kind'] = 'OtherDocument'
        self.cli('plan', success=False)
        self.assertEqual(self.writes, [])
        self.values['kind'] = 'SiteSensitiveValues'
        (self.bin / 'sops').write_text('#!/usr/bin/env python3\nimport sys\n'
                                      'print("private 192.0.2.10 fixture-token", file=sys.stderr)\n'
                                      'sys.exit(1)\n')
        self.cli('plan', success=False)
        self.assertEqual(self.writes, [])

    def test_changed_inputs_during_backup_gate_reject_before_mutation(self):
        self.bootstrap()
        self.policy['spec']['ttl'] = 301
        self.write_policy()
        age = self.bin / 'age'
        age.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
path = Path(os.environ['FIXTURE_GATE_TARGET'])
if path.name == 'token':
    path.write_text('rotated-token')
else:
    data = json.loads(path.read_text())
    if path.name == 'network.json':
        data['spec']['ttl'] = 302
    elif path.name == 'connection.json':
        data['instance'] = 'raspberrypi-dns'
    else:
        data['records'][0]['ttl'] = 999
    path.write_text(json.dumps(data))
os.execv(os.environ['FIXTURE_REAL_AGE'], ['age', *sys.argv[1:]])
''')
        age.chmod(0o755)
        for target in (self.site / 'network.json', self.state, self.connection, self.token):
            with self.subTest(target=target.name):
                original = target.read_bytes()
                plan = self.cli('plan')
                count = len(self.writes)
                self.gate_target = target
                original_zones = copy.deepcopy(self.zones)
                original_state = self.state.read_bytes()
                try:
                    self.cli('apply', '--plan', plan['plan'], success=False)
                    self.assertEqual(len(self.writes), count)
                finally:
                    target.write_bytes(original)
                    self.state.write_bytes(original_state)
                    self.zones = original_zones

    def test_other_dhcp_suffix_cannot_bypass_declared_ptr_protection(self):
        self.bootstrap()
        self.scopes = [{'name': 'other-suffix', 'domainName': 'local', 'enabled': True,
                        'dnsUpdates': True, 'dnsOverwriteForDynamicLease': False}]
        plan = self.cli('plan')
        self.assertIn('DHCP_DNS_UPDATES_REQUIRE_COLLISION_PROTECTION', plan['blockers'])
        count = len(self.writes)
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.cli('verify', success=False)
        self.assertEqual(len(self.writes), count)


if __name__ == '__main__':
    unittest.main()
