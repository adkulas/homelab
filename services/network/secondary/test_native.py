"""Public staging CLI: reject unsafe inputs before producing privileged artifacts."""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CLI = Path(__file__).with_name('native.py')


class NativeStagingTests(unittest.TestCase):
    def test_bad_download_cannot_produce_an_installable_bundle(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / 'untrusted.tar.gz'
            archive.write_bytes(b'untrusted download')
            output = root / 'bundle'
            result = subprocess.run([sys.executable, '-B', str(CLI), 'stage',
                                     '--app-archive', str(archive), '--runtime-archive', str(archive),
                                     '--output', str(output), '--recursion-acl', '192.0.2.0/24',
                                     '--management-acl', '192.0.2.0/24'],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 64, result.stderr)
            self.assertIn('checksum', result.stderr)
            self.assertFalse(output.exists())

class NativeInputTests(unittest.TestCase):
    def test_unrestricted_or_invalid_acl_is_rejected_without_output(self):
        for acl in ('0.0.0.0/0', '::/0', 'invalid', '192.0.2.0/24\nBAD=1'):
            with self.subTest(acl=acl), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / 'bundle'
                result = subprocess.run([sys.executable, '-B', str(CLI), 'stage',
                    '--app-archive', '/unavailable', '--runtime-archive', '/unavailable',
                    '--output', str(output), '--recursion-acl', acl,
                    '--management-acl', '192.0.2.0/24'], capture_output=True, text=True)
                self.assertEqual(result.returncode, 64, result.stderr)
                self.assertIn('ACL', result.stderr)
                self.assertNotIn(acl, result.stderr)
                self.assertFalse(output.exists())


if __name__ == '__main__':
    unittest.main()
