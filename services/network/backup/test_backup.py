"""Operator command tests with real encryption and a TLS Technitium boundary."""
import io
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import zipfile

CLI = Path(__file__).with_name('backup.py')


def zip_bytes(files):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, 'w') as archive:
        for name, value in files.items():
            archive.writestr(name, value)
    return stream.getvalue()


class BackupCommands(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='network-backup-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.identity = self.root / 'recovery.key'
        key = subprocess.run(['age-keygen', '-o', str(self.identity)], capture_output=True, check=True)
        self.recipient = key.stderr.decode().split('Public key: ')[1].strip()
        self.files = {'auth.config': b'authentication', 'cluster.config': b'cluster',
                      'dns.config': b'dns', 'webservice.config': b'web',
                      'zones/home.zone': b'zone', 'scopes/household.scope': b'SC\x0a\x09household\x01opaque-state'}
        self.payload = zip_bytes(self.files)
        self.requests = []
        self.fail_secondary = False
        self.cluster_initialized = True
        self.live_scopes = [{'name': 'household', 'enabled': True}]
        self.live_zones = [{'name': 'home', 'type': 'Primary'}]
        cert, key_path = self.root / 'tls.crt', self.root / 'tls.key'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(key_path), '-out', str(cert), '-days', '1',
                        '-subj', '/CN=localhost', '-addext', 'subjectAltName=IP:127.0.0.1'],
                       check=True, capture_output=True)
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                owner.requests.append((self.path, self.headers.get('Authorization')))
                self.send_response(200)
                self.end_headers()
                if owner.fail_secondary and self.headers.get('Authorization') == 'Bearer secondary-secret':
                    self.wfile.write(b'{"status":"error","errorMessage":"private-address-secret"}')
                elif self.path.startswith('/api/settings/backup?'):
                    self.wfile.write(owner.payload)
                else:
                    data = {'settings/get': {'version': '15.6.2'},
                            'admin/cluster/state': {'clusterInitialized': owner.cluster_initialized},
                            'dhcp/scopes/list': {'scopes': owner.live_scopes},
                            'zones/list': {'zones': owner.live_zones}}[self.path.removeprefix('/api/')]
                    self.wfile.write(json.dumps({'status': 'ok', 'response': data}).encode())

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key_path)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.config = self.root / 'backup.json'
        nodes = {}
        for node in ('homelab', 'raspberrypi'):
            token = self.root / (node + '.token')
            token.write_text('primary-secret' if node == 'homelab' else 'secondary-secret')
            token.chmod(0o600)
            nodes[node] = {'url': f'https://127.0.0.1:{server.server_port}',
                           'tokenFile': str(token), 'caFile': str(cert)}
        self.config.write_text(json.dumps({'nodes': nodes, 'recipients': [self.recipient]}))
        self.config.chmod(0o600)
        self.archive = self.root / 'backup.age'

    def cli(self, *args, ok=True):
        result = subprocess.run([sys.executable, '-B', str(CLI), *map(str, args)], capture_output=True, text=True)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn('secret', result.stderr)
        return result

    def create(self):
        return self.cli('create', '--config', self.config, '--output', self.archive, '--identity', self.identity)

    def unpack(self):
        result = subprocess.run(['age', '-d', '-i', str(self.identity), str(self.archive)],
                                capture_output=True, check=True)
        return zipfile.ZipFile(io.BytesIO(result.stdout))

    def rewrite_bundle(self, change):
        with self.unpack() as archive:
            files = {name: archive.read(name) for name in archive.namelist()}
        change(files)
        encrypted = subprocess.run(['age', '-r', self.recipient], input=zip_bytes(files),
                                   capture_output=True, check=True).stdout
        self.archive.write_bytes(encrypted)

    def test_failed_second_node_or_missing_live_files_never_publishes_copy(self):
        self.fail_secondary = True
        self.cli('create', '--config', self.config, '--output', self.archive,
                 '--identity', self.identity, ok=False)
        self.assertFalse(self.archive.exists())
        self.fail_secondary = False
        for member in ('auth.config', 'cluster.config', 'zones/home.zone', 'scopes/household.scope'):
            with self.subTest(member=member):
                self.payload = zip_bytes({key: value for key, value in self.files.items() if key != member})
                self.cli('create', '--config', self.config, '--output', self.archive,
                         '--identity', self.identity, ok=False)
                self.assertFalse(self.archive.exists())

    def test_gate_requires_decryption_integrity_and_freshness(self):
        self.create()
        arguments = ('gate', '--archive', self.archive, '--identity', self.identity, '--max-age-hours', '24')
        self.assertEqual(self.cli(*arguments)['status'], 'verified')
        def stale(files):
            manifest = json.loads(files['manifest.json'])
            manifest['created'] = '2000-01-01T00:00:00+00:00'
            files['manifest.json'] = json.dumps(manifest).encode()
        self.rewrite_bundle(stale)
        self.cli(*arguments, ok=False)
        self.assertEqual(self.cli('verify', '--archive', self.archive, '--identity', self.identity)['status'], 'verified')
        self.rewrite_bundle(lambda files: files.update({'homelab.zip': b'corrupt'}))
        self.cli('verify', '--archive', self.archive, '--identity', self.identity, ok=False)
        self.cli(*arguments, ok=False)

    def test_wrong_identity_or_recipient_and_unsafe_zip_fail_closed(self):
        wrong = self.root / 'wrong.key'
        subprocess.run(['age-keygen', '-o', str(wrong)], capture_output=True, check=True)
        self.cli('create', '--config', self.config, '--output', self.archive, '--identity', wrong, ok=False)
        self.assertFalse(self.archive.exists())
        self.payload = zip_bytes({**self.files, '../escape': b'bad'})
        self.cli('create', '--config', self.config, '--output', self.archive,
                 '--identity', self.identity, ok=False)
        self.assertFalse(self.archive.exists())

    def test_restore_plan_is_read_only_exact_and_requires_confirmation(self):
        self.create()
        plan = self.root / 'restore-plan.json'
        output = self.root / 'recovered'
        self.cli('restore-plan', '--archive', self.archive, '--identity', self.identity,
                 '--node', 'homelab', '--output', output, '--plan', plan, '--mode', 'live')
        self.assertFalse(output.exists())
        contents = json.loads(plan.read_text())
        self.assertEqual(contents['dhcp'], 'disabled')
        self.assertEqual(contents['mode'], 'live')
        self.assertEqual(contents['node'], 'homelab')
        report = self.cli('restore', '--plan', plan, '--identity', self.identity)
        self.assertEqual(report['status'], 'confirmation-required')
        self.assertFalse(output.exists())
        self.archive.write_bytes(self.archive.read_bytes() + b'changed')
        self.cli('restore', '--plan', plan, '--identity', self.identity, '--confirm', ok=False)
        self.assertFalse(output.exists())

    def test_restore_plan_rejects_unrecognized_scope_serialization_before_docker(self):
        self.files['scopes/household.scope'] = b'SC\x0b\x09household\x01opaque-state'
        self.payload = zip_bytes(self.files)
        self.create()
        plan = self.root / 'unsupported-plan.json'
        target = self.root / 'unsupported-recovery'
        self.cli('restore-plan', '--archive', self.archive, '--identity', self.identity,
                 '--node', 'homelab', '--output', target, '--plan', plan, '--mode', 'drill', ok=False)
        self.assertFalse(plan.exists())
        self.assertFalse(target.exists())

    def test_both_nodes_encrypted_with_manifest_and_verified_on_selected_copy(self):
        self.create()
        self.assertTrue(self.archive.read_bytes().startswith(b'age-encryption.org/v1'))
        self.assertEqual(self.archive.stat().st_mode & 0o777, 0o600)
        with self.unpack() as archive:
            manifest = json.loads(archive.read('manifest.json'))
            self.assertRegex(manifest['commit'], r'^[a-f0-9]{40}$')
            self.assertEqual(set(manifest['nodes']), {'homelab', 'raspberrypi'})
            for node in manifest['nodes'].values():
                self.assertEqual(node['version'], '15.6.2')
                self.assertEqual(set(node['files']), set(self.files))
        report = self.cli('verify', '--archive', self.archive, '--identity', self.identity)
        self.assertEqual(report['status'], 'verified')
        listing = self.cli('list', '--directory', self.root)
        self.assertEqual(listing['copies'], ['backup.age'])
        self.assertEqual(len([r for r in self.requests if 'settings/backup?' in r[0]]), 2)
        self.assertTrue(all('token=' not in path and auth.startswith('Bearer ') for path, auth in self.requests))


if __name__ == '__main__':
    unittest.main()
