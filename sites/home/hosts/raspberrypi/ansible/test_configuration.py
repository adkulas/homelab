import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from apply_configuration import unpack


class ConfigurationTests(unittest.TestCase):
    def archive(self, root, members):
        archive = root / 'configuration.tar.gz'
        with tarfile.open(archive, 'w:gz') as stream:
            for name, data in members.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                stream.addfile(member, io.BytesIO(data))
        return archive, hashlib.sha256(archive.read_bytes()).hexdigest()

    def test_accepts_disabled_scope_export(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            archive, digest = self.archive(root, {'dns.config': b'config', 'scopes/test.scope': b'SC\x0a\x01a\x00payload'})
            unpack(archive, digest, root / 'output')
            self.assertEqual((root / 'output/dns.config').read_bytes(), b'config')

    def test_rejects_enabled_scope_and_unknown_format(self):
        for data in (b'SC\x0a\x01a\x01payload', b'SC\x09\x01a\x00payload'):
            with self.subTest(data=data), tempfile.TemporaryDirectory() as work:
                root = Path(work)
                archive, digest = self.archive(root, {'dns.config': b'config', 'scopes/test.scope': data})
                with self.assertRaises(ValueError):
                    unpack(archive, digest, root / 'output')

    def test_rejects_changed_archive_before_extracting(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            archive, _ = self.archive(root, {'dns.config': b'config'})
            with self.assertRaises(ValueError):
                unpack(archive, '0' * 64, root / 'output')
            self.assertFalse((root / 'output').exists())

    def test_rejects_path_traversal(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            archive, digest = self.archive(root, {'../outside': b'bad'})
            with self.assertRaises(ValueError):
                unpack(archive, digest, root / 'output')
            self.assertFalse((root / 'outside').exists())

    def test_rejects_symlink_members(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            archive = root / 'configuration.tar.gz'
            with tarfile.open(archive, 'w:gz') as stream:
                member = tarfile.TarInfo('tls')
                member.type = tarfile.SYMTYPE
                member.linkname = '/etc'
                stream.addfile(member)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            with self.assertRaises(ValueError):
                unpack(archive, digest, root / 'output')

    def test_rejects_incomplete_export(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            archive, digest = self.archive(root, {'unrelated': b'data'})
            with self.assertRaises(ValueError):
                unpack(archive, digest, root / 'output')
