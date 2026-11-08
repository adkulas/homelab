"""Operator command checks at an isolated HTTP boundary."""
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

DNS = Path(__file__).resolve().parents[1] / 'dns'
sys.path.insert(0, str(DNS))
import test_dns as fixtures

CLI = Path(__file__).with_name('dhcp.py')


class DHCPTests(unittest.TestCase):
    setUp = fixtures.DNSTests.setUp
    write_backup = fixtures.DNSTests.write_backup
    write_policy = fixtures.DNSTests.write_policy
    api = fixtures.DNSTests.api

    def cli(self, command, *extra, success=True):
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                   FIXTURE_VALUES=json.dumps(self.values), FIXTURE_COMMIT=self.commit)
        result = subprocess.run([sys.executable, '-B', str(CLI), command,
            '--site', str(self.site), '--connection', str(self.connection),
            '--state', str(self.state), '--isolated', *extra], env=env,
            capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stderr)
        for secret in ('192.0.2.', 'fixture-token', '02-AA'):
            self.assertNotIn(secret, result.stdout + result.stderr)
        return json.loads(result.stdout) if success else result

    def topology(self):
        import yaml
        path = self.site / 'site.yaml'
        site = yaml.safe_load(path.read_text())
        site['spec']['network'].update(gateway='gateway', dhcp={
            'owner': 'homelab-dns', 'reservationStart': 'rs', 'reservationEnd': 're',
            'dynamicStart': 'ds', 'dynamicEnd': 'de',
            'reservations': [{'name': 'laptop', 'address': 'laptop', 'mac': 'mac'}]})
        path.write_text(yaml.safe_dump(site))
        self.values['spec']['values'] += [dict(id=key, value=value) for key, value in {
            'gateway': '192.0.2.1', 'rs': '192.0.2.20', 're': '192.0.2.99',
            'ds': '192.0.2.100', 'de': '192.0.2.200', 'laptop': '192.0.2.30',
            'mac': '02:aa:00:00:00:30'}.items()]

    def test_dry_run_is_protected_and_never_sets_scope(self):
        self.topology()
        plan = self.cli('plan')
        self.assertTrue(plan['protected'])
        self.assertIn({'id': 'scope:home', 'action': 'create'}, plan['changes'])
        self.cli('apply', success=False)
        self.cli('apply', '--plan', '0' * 64, success=False)
        self.cli('apply', '--plan', plan['plan'], success=False)  # no backup
        self.assertEqual(self.writes, [])

    def configured_scope(self):
        # Independent worked example of the validated fixture topology.
        return {'name': 'home', 'enabled': False, 'startingAddress': '192.0.2.20',
            'endingAddress': '192.0.2.200', 'subnetMask': '255.255.255.0',
            'routerAddress': '192.0.2.1', 'dnsServers': ['192.0.2.10', '192.0.2.11'],
            'useThisDnsServer': False, 'domainName': 'home', 'domainSearchList': ['home'],
            'dnsUpdates': True, 'dnsOverwriteForDynamicLease': False,
            'leaseTimeDays': 1, 'leaseTimeHours': 0, 'leaseTimeMinutes': 0,
            'allowOnlyReservedLeases': False, 'blockLocallyAdministeredMacAddresses': False,
            'ignoreClientIdentifierOption': True,
            'exclusions': [{'startingAddress': '192.0.2.20', 'endingAddress': '192.0.2.99'}],
            'reservedLeases': [{'hostName': 'laptop', 'hardwareAddress': '02-AA-00-00-00-30',
                               'address': '192.0.2.30', 'comments': 'Site reservation: laptop'}]}

    def test_unchanged_apply_is_idempotent_and_options_drift_is_precise(self):
        self.topology()
        self.scopes = [self.configured_scope()]
        self.assertEqual(self.cli('plan')['changes'], [])
        self.cli('apply')
        self.cli('apply')
        self.cli('verify')
        self.assertEqual(self.writes, [])
        self.scopes[0]['dnsServers'].reverse()
        self.assertEqual(self.cli('plan')['changes'], [{'id': 'scope:home', 'action': 'update-options'}])
        self.cli('verify', success=False)

    def test_scope_and_reservation_lifecycle_is_exact_and_unknown_is_preserved(self):
        self.topology()
        self.scopes = [self.configured_scope()]
        self.cli('apply')  # publish verified ownership
        self.scopes[0]['reservedLeases'][0]['address'] = '192.0.2.31'
        self.assertEqual(self.cli('plan')['changes'], [{'id': 'reservation:laptop', 'action': 'update'}])
        self.scopes[0]['reservedLeases'].append({'hostName': 'manual', 'hardwareAddress': '02-AA-00-00-00-31',
                                               'address': '192.0.2.32', 'comments': ''})
        plan = self.cli('plan')
        self.assertIn('DHCP_UNKNOWN_RESERVATIONS_REQUIRE_REVIEW', plan['blockers'])
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(len(self.scopes[0]['reservedLeases']), 2)
        self.assertEqual(self.writes, [])

    def test_invalid_topology_and_unsafe_live_state_fail_closed(self):
        self.topology()
        for field, bad in [('gateway', '192.0.2.10'), ('de', '198.51.100.10'),
                           ('ds', '192.0.2.90'), ('mac', '01:aa:00:00:00:30'),
                           ('laptop', '192.0.2.101')]:
            entry = next(item for item in self.values['spec']['values'] if item['id'] == field)
            good = entry['value']
            entry['value'] = bad
            self.cli('plan', success=False)
            entry['value'] = good
        self.scopes = [self.configured_scope()]
        self.scopes[0]['enabled'] = True
        self.assertIn('DHCP_ALLOCATION_MUST_BE_DISABLED', self.cli('plan')['blockers'])
        self.cli('apply', success=False)
        self.scopes[0]['enabled'] = False
        self.leases = [{'scope': 'home', 'address': '192.0.2.100'}]
        self.assertIn('DHCP_RETAINED_LEASES_REQUIRE_HANDOFF', self.cli('plan')['blockers'])
        self.fail_lease_read = True
        self.cli('plan', success=False)
        self.assertEqual(self.writes, [])

    def test_stale_approval_backup_failure_and_endpoint_binding_prevent_writes(self):
        self.topology()
        plan = self.cli('plan')['plan']
        self.commit = 'b' * 40
        self.cli('apply', '--plan', plan, '--backup-archive', str(self.backup),
                 '--backup-identity', str(self.identity), success=False)
        self.commit = 'a' * 40
        self.backup.write_bytes(b'corrupt')
        self.cli('apply', '--plan', plan, '--backup-archive', str(self.backup),
                 '--backup-identity', str(self.identity), success=False)
        connection = json.loads(self.connection.read_text())
        connection['instance'] = 'raspberrypi-dns'
        self.connection.write_text(json.dumps(connection))
        self.cli('plan', success=False)
        self.assertEqual(self.writes, [])

    @unittest.skipUnless(os.environ.get('HOMELAB_LIVE_DHCP') == '1', 'optional pinned Docker rehearsal')
    def test_pinned_disabled_import_exact_apply_and_second_run(self):
        import secrets
        import threading
        from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
        sys.path.insert(0, str(CLI.parent))
        import dhcp
        backup = dhcp.backup
        identifier = 'technitium-dhcp-check-' + secrets.token_hex(8)
        network = identifier + '-net'
        backup.command(['docker', 'network', 'create', '--internal', network])
        self.addCleanup(backup.command, ['docker', 'network', 'rm', network])
        password = secrets.token_hex(32)
        environment = self.root / 'environment'
        backup.write_private(environment, ('DNS_SERVER_ADMIN_PASSWORD=' + password + '\n').encode())
        backup.command(['docker', 'create', '--name', identifier, '--network', network,
            '--mount', 'type=volume,destination=/etc/dns', '--env-file', environment, backup.IMAGE])
        self.addCleanup(backup.command, ['docker', 'rm', '-f', '-v', identifier])
        backup.command(['docker', 'start', identifier])
        target = backup.isolated_api(identifier, network, password)
        # The CLI permits isolated HTTP only on loopback. Proxy only this disposable
        # target so the production URL restrictions remain exercised unchanged.
        class Proxy(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                path = self.path.removeprefix('/api/')
                if path.startswith('dhcp/scopes/') and not path.endswith(('/get', '/list')):
                    raise AssertionError('CLI sent an unsafe direct DHCP write')
                body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
                reply = target.request(path, body, self.headers.get('Content-Type'))
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(reply)
        proxy = ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
        threading.Thread(target=proxy.serve_forever, daemon=True).start()
        self.addCleanup(proxy.server_close)
        self.addCleanup(proxy.shutdown)
        self.connection.write_text(json.dumps({'instance': 'homelab-dns',
            'url': f'http://127.0.0.1:{proxy.server_port}', 'tokenFile': str(self.token)}))
        self.topology()
        plan = self.cli('plan')
        self.cli('apply', '--plan', plan['plan'], '--backup-archive', str(self.backup),
                 '--backup-identity', str(self.identity))
        self.assertEqual(self.cli('plan')['changes'], [])
        self.cli('apply')
        self.cli('verify')
        actual = target.call('dhcp/scopes/get', {'name': 'home'})
        expected = self.configured_scope()
        del expected['enabled']
        self.assertEqual(dhcp.canonical(actual, expected), expected)
        backup.command(['docker', 'restart', identifier])
        target = backup.isolated_api(identifier, network, password)
        self.assertFalse(any(scope['enabled'] for scope in target.call('dhcp/scopes/list')['scopes']))
        self.cli('verify')

    def test_nondefault_unowned_options_block_scope_replacement(self):
        self.topology()
        self.scopes = [self.configured_scope()]
        self.scopes[0]['pingCheckEnabled'] = True
        self.assertIn('DHCP_UNOWNED_OPTIONS_REQUIRE_REVIEW', self.cli('plan')['blockers'])
        self.assertEqual(self.writes, [])

    def test_owned_removal_addition_and_unknown_scope_overlap(self):
        import yaml
        self.topology()
        self.scopes = [self.configured_scope()]
        self.cli('apply')
        path = self.site / 'site.yaml'
        site = yaml.safe_load(path.read_text())
        site['spec']['network']['dhcp']['reservations'] = []
        path.write_text(yaml.safe_dump(site))
        self.assertEqual(self.cli('plan')['changes'], [{'id': 'reservation:laptop', 'action': 'delete'}])
        self.scopes[0]['reservedLeases'] = []
        self.cli('apply')
        site['spec']['network']['dhcp']['reservations'] = [{'name': 'laptop', 'address': 'laptop', 'mac': 'mac'}]
        path.write_text(yaml.safe_dump(site))
        self.assertEqual(self.cli('plan')['changes'], [{'id': 'reservation:laptop', 'action': 'add'}])
        other = self.configured_scope()
        other['name'] = 'private-manual'
        self.scopes.append(other)
        plan = self.cli('plan')
        self.assertIn('DHCP_UNKNOWN_SCOPE_OVERLAP', plan['blockers'])
        self.assertEqual(plan['unknown'][0]['kind'], 'scope')
        self.cli('apply', '--plan', plan['plan'], success=False)
        self.assertEqual(self.writes, [])

    def test_plan_approval_survives_cluster_timestamps_but_rejects_role_changes(self):
        self.topology()
        self.cluster = {'clusterInitialized': True, 'clusterDomain': 'cluster.home',
            'dnsServerDomain': 'homelab.cluster.home', 'clusterNodes': [
                {'name': 'homelab.cluster.home', 'type': 'Primary', 'state': 'Self'},
                {'name': 'raspberrypi.cluster.home', 'type': 'Secondary', 'state': 'Connected',
                 'lastSeen': '2026-11-07T15:00:00Z', 'configLastSynced': '2026-11-07T14:59:00Z'}]}
        initial = self.cli('plan')
        self.assertEqual(initial['blockers'], [])
        self.cluster['clusterNodes'][1].update(lastSeen='2026-11-07T15:01:00Z',
                                               configLastSynced='2026-11-07T15:00:00Z')
        self.assertEqual(self.cli('plan')['plan'], initial['plan'])
        self.cluster['clusterNodes'][1]['type'] = 'Primary'
        changed = self.cli('plan')
        self.assertNotEqual(changed['plan'], initial['plan'])
        self.assertIn('DHCP_ENDPOINT_NOT_DECLARED_PRIMARY', changed['blockers'])
        self.cli('apply', '--plan', initial['plan'], success=False)
        self.assertEqual(self.writes, [])
