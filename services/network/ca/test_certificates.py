"""Operator CLI checks with real OpenSSL, disposable keys and loopback TLS."""

import http.server
import io
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import tarfile
import threading
import unittest

CLI = Path(__file__).with_name("certificates.py")


class CertificatesTest(unittest.TestCase):
    def setUp(self):
        self.workspace = tempfile.TemporaryDirectory(prefix="home-ca-test-")
        self.addCleanup(self.workspace.cleanup)
        self.root = Path(self.workspace.name)
        self.password = self.root / "password"
        self.password.write_text("disposable-test-password\n")
        self.password.chmod(0o600)

    def cli(self, *args, ok=True):
        result = subprocess.run([sys.executable, str(CLI), *map(str, args)],
                                text=True, capture_output=True)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("disposable-test-password", result.stdout + result.stderr)
        return result

    def issue(self, name="homelab.home", suffix=""):
        request = self.root / ("request" + suffix)
        leaf = self.root / ("signed" + suffix)
        self.cli("request", "--name", name, "--output", request)
        self.cli("sign", "--name", name, "--ca", self.root / "ca",
                 "--request", request / "leaf.csr", "--output", leaf,
                 "--password-file", self.password)
        return request, leaf

    def create_root(self):
        self.cli("root", "--output", self.root / "ca", "--password-file", self.password)

    def test_both_host_certificates_have_trusted_names_and_server_purpose(self):
        self.create_root()
        self.assertIn("ENCRYPTED PRIVATE KEY", (self.root / "ca/root.key").read_text())
        for index, name in enumerate(("homelab.home", "raspberrypi.home")):
            request, leaf = self.issue(name, str(index))
            self.cli("verify", "--name", name, "--ca-cert", self.root / "ca/root.crt",
                     "--cert", leaf / "leaf.crt")
            self.cli("verify", "--name", "wrong.home", "--ca-cert", self.root / "ca/root.crt",
                     "--cert", leaf / "leaf.crt", ok=False)
            self.cli("verify", "--name", name, "--ca-cert", self.root / "ca/root.crt",
                     "--cert", leaf / "leaf.crt", "--min-days", 366, ok=False)
            self.assertEqual((request / "leaf.key").stat().st_mode & 0o777, 0o600)
            extensions = subprocess.check_output(
                ["openssl", "x509", "-in", str(leaf / "leaf.crt"), "-noout", "-text"], text=True)
            self.assertIn("CA:FALSE", extensions)
            self.assertIn("TLS Web Server Authentication", extensions)
            self.assertNotIn("TLS Web Client Authentication", extensions)


    def test_bundle_requires_matching_key_and_preserves_existing_outputs(self):
        self.create_root()
        request, leaf = self.issue()
        other, _ = self.issue("raspberrypi.home", "other")
        output = self.root / "management.pfx"
        common = ("--cert", leaf / "leaf.crt", "--ca-cert", self.root / "ca/root.crt",
                  "--name", "homelab.home", "--password-file", self.password,
                  "--output", output)
        self.cli("bundle", "--key", other / "leaf.key", *common, ok=False)
        self.assertFalse(output.exists())
        self.cli("bundle", "--key", request / "leaf.key", *common)
        self.cli("bundle", "--key", request / "leaf.key", *common, ok=False)
        checked = subprocess.run(["openssl", "pkcs12", "-in", str(output), "-noout",
                                  "-passin", "file:" + str(self.password)], capture_output=True)
        self.assertEqual(checked.returncode, 0)
        self.assertEqual(output.stat().st_mode & 0o777, 0o600)


    def test_probe_validates_trust_name_expiry_and_a_renewed_leaf(self):
        self.create_root()
        first_request, first_leaf = self.issue()
        renewed_request, renewed_leaf = self.issue(suffix="renewed")
        self.cli("root", "--output", self.root / "untrusted", "--password-file", self.password)
        serials = set()
        for request, leaf in ((first_request, first_leaf), (renewed_request, renewed_leaf)):
            serials.add(subprocess.check_output(["openssl", "x509", "-in", str(leaf / "leaf.crt"),
                                                "-noout", "-serial"]))
            server = http.server.HTTPServer(("127.0.0.1", 0), http.server.BaseHTTPRequestHandler)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(leaf / "leaf.crt", request / "leaf.key")
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                common = ("--address", "127.0.0.1", "--port", server.server_port)
                self.cli("probe", "--name", "homelab.home", "--ca-cert", self.root / "ca/root.crt", *common)
                self.cli("probe", "--name", "homelab.home", "--ca-cert", self.root / "ca/root.crt",
                         "--expected-cert", leaf / "leaf.crt", *common)
                if leaf != first_leaf:
                    self.cli("probe", "--name", "homelab.home", "--ca-cert", self.root / "ca/root.crt",
                             "--expected-cert", first_leaf / "leaf.crt", *common, ok=False)
                self.cli("probe", "--name", "wrong.home", "--ca-cert", self.root / "ca/root.crt", *common, ok=False)
                self.cli("probe", "--name", "homelab.home", "--ca-cert", self.root / "untrusted/root.crt", *common, ok=False)
                self.cli("probe", "--name", "homelab.home", "--ca-cert", self.root / "ca/root.crt",
                         "--min-days", 366, *common, ok=False)
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
        self.assertEqual(len(serials), 2, "renewal must issue a distinct certificate")


    def test_offline_root_backup_can_recover_and_sign_without_original_medium(self):
        self.create_root()
        identity = self.root / "recovery-identity"
        subprocess.run(["age-keygen", "-o", str(identity)], check=True, capture_output=True)
        recipient = subprocess.check_output(["age-keygen", "-y", str(identity)], text=True).strip()
        archive = subprocess.check_output(["tar", "-C", str(self.root / "ca"), "-cf", "-", "root.key", "root.crt"])
        encrypted = subprocess.check_output(["age", "-r", recipient], input=archive)
        self.assertNotIn(b"PRIVATE KEY", encrypted)
        decrypted = subprocess.check_output(["age", "-d", "-i", str(identity)], input=encrypted)
        restored = self.root / "restored"
        restored.mkdir(mode=0o700)
        with tarfile.open(fileobj=io.BytesIO(decrypted)) as backup:
            self.assertEqual(set(backup.getnames()), {"root.key", "root.crt"})
            for name in backup.getnames():
                (restored / name).write_bytes(backup.extractfile(name).read())
                (restored / name).chmod(0o600)
        original_cert = (self.root / "ca/root.crt").read_bytes()
        self.assertEqual((restored / "root.crt").read_bytes(), original_cert)
        (self.root / "ca/root.key").unlink()
        self.cli("request", "--name", "recovery.home", "--output", self.root / "drill")
        self.cli("sign", "--name", "recovery.home", "--ca", restored,
                 "--request", self.root / "drill/leaf.csr", "--output", self.root / "drill-leaf",
                 "--password-file", self.password)
        self.cli("verify", "--name", "recovery.home", "--ca-cert", self.root / "ca/root.crt",
                 "--cert", self.root / "drill-leaf/leaf.crt")

    def test_private_operations_refuse_git_outputs_and_public_password_files(self):
        checkout = self.root / "checkout"
        checkout.mkdir()
        (checkout / ".git").mkdir()
        result = self.cli("root", "--output", checkout / "ca", "--password-file", self.password, ok=False)
        self.assertIn("outside Git", result.stderr)
        self.assertFalse((checkout / "ca").exists())
        alias = self.root / "alias"
        alias.symlink_to(checkout, target_is_directory=True)
        self.cli("request", "--name", "homelab.home", "--output", alias / "request", ok=False)
        self.password.chmod(0o644)
        self.cli("root", "--output", self.root / "unsafe", "--password-file", self.password, ok=False)
        self.assertFalse((self.root / "unsafe").exists())


if __name__ == "__main__":
    unittest.main()
