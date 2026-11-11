"""Disposable integration checks; run via run.sh, never against household Hosts."""
import json
import http.client
import time
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(os.environ.get('TRAEFIK_TEST_REPOSITORY', '/repository'))
TASKS = ROOT / 'sites/home/hosts/homelab/ansible'


def command(*args, ok=True):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if ok and result.returncode:
        raise AssertionError(result.stdout)
    return result


class Deployment(unittest.TestCase):
    def setUp(self):
        self.work = Path(tempfile.mkdtemp(dir=os.environ['TRAEFIK_TEST_WORK']))
        self.source = self.work / 'source'
        self.deploy = self.work / 'installed'
        for path in ('services/traefik', 'sites/home/traefik'):
            shutil.copytree(ROOT / path, self.source / path)
        service = self.source / 'services/traefik/compose.yaml'
        service.write_text(service.read_text().replace('"80"', '"28080"').replace('"19134"', '"29134"'))
        self.play = self.work / 'play.yml'
        self.play.write_text(f'''---
- name: Rehearse Traefik deployment
  hosts: localhost
  gather_facts: false
  vars_files:
    - {TASKS}/vars.yml
  tasks:
    - name: Deploy Traefik
      ansible.builtin.import_tasks: {TASKS}/traefik.yml
      tags: [traefik]
''')

    def tearDown(self):
        compose = self.deploy / 'sites/home/traefik/compose.yaml'
        if compose.exists():
            # The installed candidate may deliberately be invalid; use the immutable
            # repository definition to remove this rehearsal's guarded project.
            command('docker', 'compose', '--project-name', 'home-traefik', '-f',
                    str(ROOT / 'sites/home/traefik/compose.yaml'), 'down', ok=False)
        shutil.rmtree(self.work)

    def run_play(self, *extra, ok=True):
        return command('ansible-playbook', '-i', 'localhost,', '-c', 'local', str(self.play),
                       '--tags', 'traefik', '-e', json.dumps({
                           'host_traefik_source_root': str(self.source),
                           'host_traefik_deploy_root': str(self.deploy),
                           'host_traefik_probe_url': 'http://127.0.0.1:28080/'}), *extra, ok=ok)

    def test_fresh_preview_defers_without_creating_files_or_containers(self):
        result = self.run_play('--check', '--diff')
        self.assertIn('deferred', result.stdout.lower())
        self.assertFalse(self.deploy.exists())
        self.assertEqual(command('docker', 'ps', '-aq', '--filter',
                                 'label=com.docker.compose.project=home-traefik').stdout.strip(), '')

    def container_id(self):
        return command('docker', 'ps', '-q', '--filter',
                       'label=com.docker.compose.project=home-traefik').stdout.strip()

    def test_deploys_controller_files_and_converges(self):
        self.run_play()
        first = self.container_id()
        self.assertTrue(first)
        config = json.loads(command('docker', 'compose', '-f', str(self.deploy /
                            'sites/home/traefik/compose.yaml'), 'config', '--format', 'json').stdout)
        self.assertEqual(config['name'], 'home-traefik')
        service = config['services']['traefik']
        self.assertIn('@sha256:', service['image'])
        mounts = {v['target']: v['source'] for v in service['volumes']}
        self.assertEqual(mounts['/etc/traefik/traefik.yml'], str(self.deploy / 'services/traefik/traefik.yml'))
        self.assertEqual(mounts['/etc/traefik/dynamic'], str(self.deploy / 'sites/home/traefik/dynamic'))
        self.assertEqual((self.deploy / 'services/traefik/traefik.yml').stat().st_uid, 0)
        result = self.run_play()
        self.assertIn('changed=0', result.stdout)
        self.assertEqual(first, self.container_id())
        self.run_play('--check', '--diff')
        self.assertEqual(first, self.container_id())

    def fixture_route(self, destination):
        route = self.source / 'sites/home/traefik/dynamic/fixture.yml'
        route.write_text(f"""http:
  routers:
    fixture:
      rule: Host(`fixture.home`)
      entryPoints: [web]
      service: noop@internal
      middlewares: [fixture]
  middlewares:
    fixture:
      redirectRegex:
        regex: '^http://.*'
        replacement: 'https://{destination}/'
""")
        return route

    def expect_route(self, status, location=None):
        for _ in range(30):
            connection = http.client.HTTPConnection('127.0.0.1', 28080, timeout=2)
            connection.request('GET', '/', headers={'Host': 'fixture.home'})
            response = connection.getresponse()
            actual = response.status, response.getheader('Location')
            response.read()
            connection.close()
            if actual == (status, location):
                return
            time.sleep(1)
        self.fail(f'route returned {actual}, expected {(status, location)}')

    def test_dynamic_updates_and_removals_preserve_container_and_unrelated_files(self):
        route = self.fixture_route('first.invalid')
        self.run_play()
        original = self.container_id()
        self.expect_route(302, 'https://first.invalid/')
        unrelated = self.deploy / 'sites/home/traefik/dynamic/operator.yml'
        unrelated.write_text('http:\n  middlewares:\n    operator:\n      headers:\n        customResponseHeaders:\n          X-Operator: kept\n')
        self.fixture_route('second.invalid')
        self.run_play()
        self.expect_route(302, 'https://second.invalid/')
        self.assertEqual(original, self.container_id())
        route.unlink()
        self.run_play()
        self.expect_route(404)
        self.assertFalse((self.deploy / 'sites/home/traefik/dynamic/fixture.yml').exists())
        self.assertTrue(unrelated.exists())
        self.assertEqual(original, self.container_id())

    def test_static_change_survives_failed_validation_and_recreates(self):
        self.fixture_route('first.invalid')
        self.run_play()
        original = self.container_id()
        static = self.source / 'services/traefik/traefik.yml'
        static.write_text(static.read_text() + '\nlog:\n  level: DEBUG\n')
        compose = self.source / 'sites/home/traefik/compose.yaml'
        valid = compose.read_text()
        compose.write_text(valid + '\ninvalid_compose_key: true\n')
        self.fixture_route('rejected.invalid')
        failed = self.run_play(ok=False)
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn('Validate the installed Compose project', failed.stdout)
        self.assertEqual(original, self.container_id())
        installed_route = self.deploy / 'sites/home/traefik/dynamic/fixture.yml'
        self.assertIn('first.invalid', installed_route.read_text())
        self.expect_route(302, 'https://first.invalid/')
        compose.write_text(valid)
        preview = self.run_play('--check', '--diff')
        self.assertIn('deferred', preview.stdout)
        self.assertEqual(original, self.container_id())
        self.run_play()
        self.assertNotEqual(original, self.container_id())
        inside = command('docker', 'exec', self.container_id(), 'cat', '/etc/traefik/traefik.yml').stdout
        self.assertIn('level: DEBUG', inside)
        result = self.run_play()
        self.assertIn('changed=0', result.stdout)

    def test_missing_docker_fails_clearly_and_preview_defers(self):
        binaries = self.work / 'bin'
        binaries.mkdir()
        docker = binaries / 'docker'
        docker.write_text('#!/bin/sh\necho "Docker unavailable in rehearsal" >&2\nexit 1\n')
        docker.chmod(0o755)
        original_path = os.environ['PATH']
        try:
            os.environ['PATH'] = str(binaries) + ':' + original_path
            preview = self.run_play('--check', '--diff')
            self.assertIn('deferred', preview.stdout)
            failed = self.run_play(ok=False)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('--tags docker', failed.stdout)
            self.assertFalse(self.deploy.exists())
        finally:
            os.environ['PATH'] = original_path


if __name__ == '__main__':
    unittest.main(verbosity=2)
