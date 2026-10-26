#!/usr/bin/env python3
"""Encrypted paired Technitium backups and supervised, isolated recovery."""
import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import secrets
import shutil
import tempfile
import ssl
import subprocess
import sys
import time
import urllib.parse
import urllib.request
import zipfile

PIN = Path(__file__).resolve().parents[1] / 'technitium.env'
IMAGE = PIN.read_text().strip().split('=', 1)[1]
VERSION = IMAGE.split('@', 1)[0].rsplit(':', 1)[1]
NODES = {'homelab', 'raspberrypi'}
COMPONENTS = ('authConfig', 'clusterConfig', 'webServiceSettings', 'dnsSettings',
              'logSettings', 'zones', 'allowedZones', 'blockedZones', 'blockLists', 'apps', 'scopes')
MAX_BYTES = 512 * 1024 * 1024


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def command(args, data=None):
    result = subprocess.run(list(map(str, args)), input=data, capture_output=True)
    if result.returncode:
        raise ValueError('external operation failed; check credentials, tools and storage')
    return result.stdout


def outside_git(value):
    path = Path(value).absolute()
    if path != path.resolve() or any((parent / '.git').exists() for parent in (path, *path.parents)):
        raise ValueError('private files must be outside Git checkouts and contain no symlinks')
    return path


def private_file(value):
    path = outside_git(value)
    if not path.is_file() or path.stat().st_mode & 0o077:
        raise ValueError('private input must be a regular file with mode 0600')
    return path


def fresh_output(value):
    path = outside_git(value)
    if path.exists() or not path.parent.is_dir():
        raise ValueError('output must be fresh with an existing parent')
    return path


def write_private(path, data):
    with open(path, 'xb') as output:
        os.chmod(path, 0o600)
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError('API redirects are forbidden')


class Api:
    def __init__(self, url, token, ca=None, isolated=False):
        parsed = urllib.parse.urlsplit(url)
        if (parsed.username or parsed.password or parsed.query or parsed.fragment
                or parsed.path not in ('', '/') or not parsed.hostname
                or parsed.scheme != ('http' if isolated else 'https')):
            raise ValueError('API requires a plain HTTPS origin without credentials')
        self.base = url.rstrip('/') + '/api/'
        self.token = token
        context = ssl.create_default_context(cafile=ca)
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPSHandler(context=context))

    def request(self, path, data=None, content_type=None):
        headers = {'Authorization': 'Bearer ' + self.token} if self.token else {}
        if content_type:
            headers['Content-Type'] = content_type
        request = urllib.request.Request(self.base + path, data=data, headers=headers)
        with self.opener.open(request, timeout=60) as response:
            result = response.read(MAX_BYTES + 1)
        if len(result) > MAX_BYTES:
            raise ValueError('API archive exceeds size limit')
        return result

    def call(self, path, values=None):
        data = urllib.parse.urlencode(values).encode() if values is not None else None
        response = json.loads(self.request(path, data, 'application/x-www-form-urlencoded'))
        if response.get('status') != 'ok':
            raise ValueError('Technitium API rejected operation')
        return response.get('response', response)


def zip_files(data):
    if len(data) > MAX_BYTES:
        raise ValueError('archive exceeds size limit')
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        entries = archive.infolist()
        if sum(entry.file_size for entry in entries) > MAX_BYTES:
            raise ValueError('expanded archive exceeds size limit')
        names = [entry.filename for entry in entries]
        if len(names) != len(set(names)):
            raise ValueError('duplicate archive member')
        for entry in entries:
            path = PurePosixPath(entry.filename)
            if (path.is_absolute() or '..' in path.parts or '\\' in entry.filename
                    or str(path) != entry.filename.rstrip('/')
                    or (entry.external_attr >> 16) & 0o170000 == 0o120000):
                raise ValueError('unsafe archive member')
        return {entry.filename: archive.read(entry) for entry in entries if not entry.is_dir()}


def make_zip(files):
    result = io.BytesIO()
    with zipfile.ZipFile(result, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name, data in files.items():
            archive.writestr(name, data)
    return result.getvalue()


def validate_capture(files, observation):
    required = {'auth.config', 'dns.config', 'webservice.config'}
    if observation['clusterInitialized']:
        required.add('cluster.config')
    if not required.issubset(files):
        raise ValueError('backup is missing required configuration; wait for settings to flush')
    for category in ('scopes', 'zones'):
        suffix = '.scope' if category == 'scopes' else '.zone'
        if sum(name.startswith(category + '/') and name.endswith(suffix) for name in files) < observation[category]:
            raise ValueError('backup is missing live scope or zone files; wait for settings to flush')


def load_bundle(archive, identity):
    encrypted = outside_git(archive).read_bytes()
    payload = command(['age', '-d', '-i', private_file(identity)], encrypted)
    files = zip_files(payload)
    manifest = json.loads(files.pop('manifest.json'))
    if (manifest['format'] != 1 or set(manifest['nodes']) != NODES
            or set(files) != {node + '.zip' for node in NODES}):
        raise ValueError('invalid paired backup manifest')
    for node, metadata in manifest['nodes'].items():
        data = files[node + '.zip']
        contents = zip_files(data)
        if (metadata['version'] != VERSION or metadata['sha256'] != digest(data)
                or metadata['files'] != {name: digest(value) for name, value in contents.items()}):
            raise ValueError('backup checksums or version do not match')
        validate_capture(contents, metadata['observation'])
    return encrypted, manifest, files


def create(args):
    target = fresh_output(args.output)
    config = json.loads(private_file(args.config).read_text())
    if set(config['nodes']) != NODES or not config['recipients']:
        raise ValueError('backup requires both DNS Stack Instances and recovery recipients')
    private_file(args.identity)
    node_files, nodes = {}, {}
    for name, settings in config['nodes'].items():
        token = private_file(settings['tokenFile']).read_text().strip()
        if not token:
            raise ValueError('backup token is empty')
        api = Api(settings['url'], token, settings.get('caFile'))
        version = api.call('settings/get')['version']
        if version != VERSION:
            raise ValueError('backup requires the pinned Technitium version')
        observation = {
            'clusterInitialized': api.call('admin/cluster/state')['clusterInitialized'],
            'scopes': len(api.call('dhcp/scopes/list')['scopes']),
            'zones': sum(zone['type'] != 'Internal' for zone in api.call('zones/list')['zones']),
        }
        query = urllib.parse.urlencode({key: 'true' for key in COMPONENTS})
        data = api.request('settings/backup?' + query)
        files = zip_files(data)
        validate_capture(files, observation)
        node_files[name + '.zip'] = data
        nodes[name] = {'version': version, 'sha256': digest(data), 'observation': observation,
                       'files': {key: digest(value) for key, value in files.items()}}
    repository = Path(__file__).resolve().parents[3]
    commit = command(['git', '-C', repository, 'rev-parse', 'HEAD']).decode().strip()
    manifest = {'format': 1, 'created': datetime.now(timezone.utc).isoformat(),
                'commit': commit, 'nodes': nodes}
    node_files['manifest.json'] = encode(manifest)
    recipients = [argument for recipient in config['recipients'] for argument in ('-r', recipient)]
    encrypted = command(['age', *recipients], make_zip(node_files))
    # Verify decryption before publishing a complete copy at its selected destination.
    temporary = target.parent / ('.backup-' + secrets.token_hex(12) + '.age')
    try:
        write_private(temporary, encrypted)
        load_bundle(temporary, args.identity)
        os.link(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
    return {'status': 'verified', 'sha256': digest(encrypted)}


def verify(args):
    encrypted, manifest, _ = load_bundle(args.archive, args.identity)
    if args.command == 'gate':
        age = (datetime.now(timezone.utc) - datetime.fromisoformat(manifest['created'])).total_seconds()
        if args.max_age_hours <= 0 or not 0 <= age <= args.max_age_hours * 3600:
            raise ValueError('backup is stale; protected change blocked')
    return {'status': 'verified', 'sha256': digest(encrypted), 'created': manifest['created']}


def disabled_scope_files(files):
    """Clear only the enabled byte in the pinned v10 scope format before import.

    Upstream v15.6.2 Dhcp/Scope.cs WriteTo: SC, version 10, a byte-length
    UTF-8 name (StreamExtensions.WriteShortString), then a Boolean enabled.
    All reservation, option and lease bytes remain unchanged. Fail closed on
    unfamiliar formats, including older serialized scopes awaiting migration.
    """
    selected = {}
    for name, data in files.items():
        if name.startswith('scopes/'):
            if not name.endswith('.scope') or len(data) < 5 or data[:3] != b'SC\x0a':
                raise ValueError('unsupported scope serialization; supervised migration required')
            offset = 4 + data[3]
            if offset >= len(data) or data[offset] not in (0, 1):
                raise ValueError('invalid serialized DHCP scope')
            data[4:offset].decode('utf-8')
            data = data[:offset] + b'\x00' + data[offset + 1:]
            selected[name] = data
        elif name.startswith('zones/'):
            selected[name] = data
    return selected


def restore_plan(args):
    encrypted, _, files = load_bundle(args.archive, args.identity)
    disabled_scope_files(zip_files(files[args.node + '.zip']))
    target = fresh_output(args.output)
    plan = {'format': 1, 'archive': str(outside_git(args.archive)), 'sha256': digest(encrypted),
            'node': args.node, 'output': str(target), 'mode': args.mode,
            'image': IMAGE, 'dhcp': 'disabled'}
    write_private(fresh_output(args.plan), encode(plan))
    return {'status': 'planned', 'mode': args.mode, 'dhcp': 'disabled'}


def isolated_api(container, network, password):
    details = json.loads(command(['docker', 'inspect', container]))[0]
    bridge = json.loads(command(['docker', 'network', 'inspect', network]))[0]
    if (not bridge['Internal'] or bridge['Driver'] != 'bridge'
            or set(details['NetworkSettings']['Networks']) != {network}
            or any(details['NetworkSettings']['Ports'].values())
            or details['HostConfig']['Privileged']
            or set(bridge['Containers']) != {details['Id']}):
        raise ValueError('Restore Drill isolation check failed')
    address = details['NetworkSettings']['Networks'][network]['IPAddress']
    api = Api('http://' + address + ':5380', '', isolated=True)
    deadline = time.monotonic() + 60
    while True:
        try:
            api.token = api.call('user/login', {'user': 'admin', 'pass': password})['token']
            return api
        except (OSError, ValueError):
            if time.monotonic() >= deadline:
                raise ValueError('isolated recovery service did not become ready') from None
            time.sleep(1)


def recover_isolated(data, output, mode):
    """Restore scope state only inside a private bridge; export a stopped directory."""
    identifier = 'technitium-restore-' + secrets.token_hex(10)
    network, container = identifier + '-net', identifier
    created_network = created_container = False
    password = secrets.token_hex(32)
    files = zip_files(data)
    try:
        command(['docker', 'network', 'create', '--internal', network])
        created_network = True
        with tempfile.TemporaryDirectory(prefix='network-recovery-') as workspace:
            environment = Path(workspace) / 'environment'
            write_private(environment, ('DNS_SERVER_ADMIN_PASSWORD=' + password + '\n'
                                       'DNS_SERVER_DOMAIN=restore.pilot.test\n').encode())
            command(['docker', 'create', '--name', container, '--network', network,
                     '--mount', 'type=volume,destination=/etc/dns', '--env-file', environment, IMAGE])
        created_container = True
        command(['docker', 'start', container])
        api = isolated_api(container, network, password)
        # Avoid importing live addresses, cluster connections or authentication into the running sandbox.
        selected = disabled_scope_files(files)
        boundary = 'restore-' + secrets.token_hex(16)
        body = (('--' + boundary + '\r\nContent-Disposition: form-data; name="file"; '
                 'filename="restore.zip"\r\nContent-Type: application/zip\r\n\r\n').encode()
                + make_zip(selected) + ('\r\n--' + boundary + '--\r\n').encode())
        result = json.loads(api.request('settings/restore?zones=true&scopes=true&deleteExistingFiles=true',
                                       body, 'multipart/form-data; boundary=' + boundary))
        if result.get('status') != 'ok':
            raise ValueError('isolated restore failed')
        scopes = api.call('dhcp/scopes/list')['scopes']
        if len(scopes) != sum(name.startswith('scopes/') and name.endswith('.scope') for name in files):
            raise ValueError('restored scope count differs from backup')
        if any(scope['enabled'] for scope in scopes):
            raise ValueError('recovered DHCP scope was enabled during import')
        expected_zones = {PurePosixPath(name).stem for name in files
                          if name.startswith('zones/') and name.endswith('.zone')}
        actual_zones = {zone['name'] for zone in api.call('zones/list')['zones']}
        if not expected_zones.issubset(actual_zones):
            raise ValueError('restored zones are missing')
        # Restart inside the isolated network to prove disabled DHCP state survives a reload.
        command(['docker', 'restart', container])
        api = isolated_api(container, network, password)
        if any(scope['enabled'] for scope in api.call('dhcp/scopes/list')['scopes']):
            raise ValueError('recovered DHCP scopes did not stay disabled')
        command(['docker', 'stop', container])
        output.mkdir(mode=0o700)
        command(['docker', 'cp', container + ':/etc/dns/.', output])
        if mode == 'live':
            # Complete original node state is overlaid only after the sandbox has stopped.
            # Its scope files are always the disabled files exported by Technitium.
            for name, value in files.items():
                if name.startswith('scopes/'):
                    continue
                target = output / name
                target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                target.write_bytes(value)
        for path in output.rglob('*'):
            path.chmod(0o700 if path.is_dir() else 0o600)
        return {'status': 'recovered-stopped', 'mode': mode, 'dhcp': 'disabled',
                'scopes': len(scopes), 'zones': len(expected_zones)}
    finally:
        # Cleanup failures are failures of the operation; never report a successful drill.
        try:
            if created_container:
                command(['docker', 'rm', '-f', '-v', container])
        finally:
            if created_network:
                command(['docker', 'network', 'rm', network])


def restore(args):
    plan = json.loads(private_file(args.plan).read_text())
    if (set(plan) != {'format', 'archive', 'sha256', 'node', 'output', 'mode', 'image', 'dhcp'}
            or plan['format'] != 1 or plan['node'] not in NODES
            or plan['image'] != IMAGE or plan['dhcp'] != 'disabled'
            or plan['mode'] not in ('drill', 'live')):
        raise ValueError('invalid restore plan')
    target = fresh_output(plan['output'])
    encrypted, _, files = load_bundle(plan['archive'], args.identity)
    if digest(encrypted) != plan['sha256']:
        raise ValueError('backup changed after planning')
    if not args.confirm:
        return {'status': 'confirmation-required', 'mode': plan['mode'], 'dhcp': 'disabled'}
    try:
        return recover_isolated(files[plan['node'] + '.zip'], target, plan['mode'])
    except Exception:
        # A partial directory is never presented as recoverable configuration.
        if target.exists():
            shutil.rmtree(target)
        raise


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    capture = commands.add_parser('create')
    capture.add_argument('--config', required=True)
    capture.add_argument('--output', required=True)
    capture.add_argument('--identity', required=True)
    listing = commands.add_parser('list')
    listing.add_argument('--directory', required=True)
    for name in ('verify', 'gate'):
        check = commands.add_parser(name)
        check.add_argument('--archive', required=True)
        check.add_argument('--identity', required=True)
        if name == 'gate':
            check.add_argument('--max-age-hours', required=True, type=float)
    planning = commands.add_parser('restore-plan')
    planning.add_argument('--archive', required=True)
    planning.add_argument('--identity', required=True)
    planning.add_argument('--node', required=True, choices=sorted(NODES))
    planning.add_argument('--output', required=True)
    planning.add_argument('--plan', required=True)
    planning.add_argument('--mode', required=True, choices=('drill', 'live'))
    recovery = commands.add_parser('restore')
    recovery.add_argument('--plan', required=True)
    recovery.add_argument('--identity', required=True)
    recovery.add_argument('--confirm', action='store_true')
    args = parser.parse_args()
    try:
        if args.command == 'create':
            result = create(args)
        elif args.command == 'restore-plan':
            result = restore_plan(args)
        elif args.command == 'restore':
            result = restore(args)
        elif args.command == 'list':
            directory = outside_git(args.directory)
            result = {'copies': sorted(path.name for path in directory.glob('*.age') if path.is_file())}
        else:
            result = verify(args)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile, subprocess.SubprocessError):
        print('network backup operation failed; protected changes must stop. Check private inputs, archive integrity, tool availability and storage.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
