"""Operator command tests with fixture responses at the operating-system boundary."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
PREFLIGHT = ROOT / "services/network/preflight.py"


class DeploymentPreflightTests(unittest.TestCase):
    def run_preflight(self, sockets="", failed="", evidence=True, routes=True, docker_subnet="172.18.0.0/16"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            probe = root / "probe"
            probe.write_text('''#!/usr/bin/env python3
import json, os, sys
name = os.path.basename(sys.argv[0])
if name == os.environ.get("FAILED_PROBE"):
    sys.exit(1)
if name == "ip":
    if "route" in sys.argv:
        print(os.environ["ROUTES"])
    else:
        print('[{"ifname":"eth0","addr_info":[{"scope":"global","local":"192.0.2.10","prefixlen":24}]}]')
elif name == "ss":
    print(os.environ["SOCKETS"])
elif name == "resolvectl":
    print("Link 2: 192.0.2.1")
elif name == "docker":
    if "ls" in sys.argv:
        print("fixture-network")
    else:
        print(json.dumps([{"IPAM": {"Config": [{"Subnet": os.environ["DOCKER_SUBNET"]}]}}]))
''')
            probe.chmod(0o755)
            for name in ("ip", "ss", "resolvectl", "git", "docker"):
                (root / name).symlink_to(probe)
            confirmations = root / "evidence.json"
            # The report defines the public operator confirmations; no private implementation imports.
            environment = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                               SOCKETS=sockets, FAILED_PROBE=failed, DOCKER_SUBNET=docker_subnet,
                               ROUTES=json.dumps([{"dev": "eth0", "gateway": "192.0.2.1"}] if routes else []))
            args = ["python3", "-B", str(PREFLIGHT), "--role", "homelab", "--deployment"]
            initial = subprocess.run(args, env=environment, capture_output=True, text=True)
            if not evidence:
                return initial
            self.assertIn(initial.returncode, (0, 2), initial.stderr)
            report = json.loads(initial.stdout)
            confirmations.write_text(json.dumps({item["code"].lower(): True for item in report["blockers"]}))
            return subprocess.run(args + ["--evidence", str(confirmations)], env=environment,
                                  capture_output=True, text=True)

    def test_deployment_blocks_conflicting_listeners_even_when_operator_confirms(self):
        for protocol, port in (("tcp", 53), ("udp", 53), ("udp", 67), ("tcp", 5380), ("tcp", 53443)):
            with self.subTest(protocol=protocol, port=port):
                result = self.run_preflight(f"{protocol} LISTEN 0 4096 [::]:{port} [::]:*")
                self.assertEqual(result.returncode, 2, result.stderr)
                report = json.loads(result.stdout)
                self.assertIn("DEPLOYMENT_PORT_CONFLICT", [item["code"] for item in report["blockers"]])
                self.assertNotIn("192.0.2.", result.stdout)
                self.assertNotIn("[::]", result.stdout)

    def test_missing_probes_routes_or_confirmations_block_deployment(self):
        for failure in ("ip", "ss", "git", "docker"):
            with self.subTest(failure=failure):
                result = self.run_preflight(failed=failure)
                self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(self.run_preflight(routes=False).returncode, 2)
        result = self.run_preflight(evidence=False)
        self.assertEqual(result.returncode, 2)
        codes = {item["code"] for item in json.loads(result.stdout)["blockers"]}
        self.assertTrue({"DEPLOYMENT_ROUTES_REVIEWED", "DEPLOYMENT_FIREWALL_READY",
                         "DEPLOYMENT_ROLLBACK_READY", "DEPLOYMENT_FRESH_CONFIGURATION"} <= codes)

    def test_docker_subnet_overlapping_lan_is_a_non_waivable_route_conflict(self):
        result = self.run_preflight(docker_subnet="192.0.2.0/24")
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("DEPLOYMENT_ROUTE_CONFLICT", [item["code"] for item in json.loads(result.stdout)["blockers"]])
        self.assertNotIn("192.0.2.", result.stdout)

    def test_clear_confirmed_host_passes_without_exposing_addresses(self):
        result = self.run_preflight()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["readyForNetworkChange"])
        self.assertNotIn("192.0.2.", result.stdout)


class PrimaryComposeTests(unittest.TestCase):
    def test_rendered_service_has_private_persistent_state_and_restricted_bootstrap(self):
        environment = dict(os.environ, PRIMARY_CONFIG_DIR="/private/dns/config",
                           PRIMARY_PASSWORD_FILE="/private/dns/admin-password",
                           PRIMARY_RECURSION_ACL="192.0.2.0/24,2001:db8::/64")
        result = subprocess.run([
            "docker", "compose", "--env-file", "services/network/technitium.env",
            "-f", "services/network/compose.yaml", "config", "--format", "json",
        ], cwd=ROOT, env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        compose = json.loads(result.stdout)
        self.assertEqual(set(compose["services"]), {"primary"})
        service = compose["services"]["primary"]
        self.assertEqual(compose["name"], "homelab-dns")
        self.assertIn("dns-server:15.6.2@sha256:", service["image"])
        self.assertEqual(service["network_mode"], "host")
        self.assertFalse(service.get("ports"))
        self.assertFalse(service.get("privileged", False))
        self.assertEqual(service["restart"], "unless-stopped")
        self.assertEqual(len(service["volumes"]), 1)
        volume = service["volumes"][0]
        self.assertEqual((volume["type"], volume["source"], volume["target"]),
                         ("bind", "/private/dns/config", "/etc/dns"))
        self.assertFalse(volume.get("bind", {}).get("create_host_path", False))
        settings = service["environment"]
        self.assertEqual(settings["DNS_SERVER_WEB_SERVICE_LOCAL_ADDRESSES"], "127.0.0.1")
        self.assertEqual(settings["DNS_SERVER_RECURSION"], "UseSpecifiedNetworkACL")
        self.assertEqual(settings["DNS_SERVER_RECURSION_NETWORK_ACL"], "192.0.2.0/24,2001:db8::/64")
        self.assertEqual(settings["DNS_SERVER_ADMIN_PASSWORD_FILE"], "/run/secrets/admin_password")
        self.assertNotIn("DNS_SERVER_ADMIN_PASSWORD", settings)
        self.assertNotIn("DNS_SERVER_FORWARDERS", settings)
        self.assertNotIn("DNS_SERVER_WEB_SERVICE_TLS_CERTIFICATE_PASSWORD", settings)
        self.assertEqual(compose["secrets"]["admin_password"]["file"], "/private/dns/admin-password")


if __name__ == "__main__":
    unittest.main()
