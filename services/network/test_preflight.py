import importlib.util
import json
import unittest
from pathlib import Path
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location(
    "network_preflight", Path(__file__).with_name("preflight.py")
)
preflight = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preflight)


class PreflightTests(unittest.TestCase):
    def test_report_redacts_observed_addresses(self):
        evidence = {key: True for key in preflight.SITE_CHECKS | preflight.HOST_CHECKS["homelab"]}
        with (
            patch.object(preflight, "json_command", side_effect=[
                (True, [{"dst": "default", "dev": "enp2s0f1", "gateway": "192.0.2.1"}]),
                (True, [{"ifname": "enp2s0f1", "addr_info": [
                    {"scope": "global", "local": "192.0.2.10"}
                ]}]),
            ]),
            patch.object(preflight, "command", side_effect=[
                (True, "udp UNCONN 0 0 0.0.0.0:67 0.0.0.0:*"),
                (True, "Link 2: 2001:db8::1"),
            ]),
            patch.object(preflight, "address_candidates", return_value=[]),
            patch.object(preflight, "profile_file_count", return_value=1),
        ):
            report = preflight.collect("homelab", Path("/tmp"), evidence)
        serialized = json.dumps(report)
        self.assertNotIn("192.0.2.", serialized)
        self.assertNotIn("2001:db8", serialized)
        self.assertTrue(report["inventory"]["relevantListeners"]["dhcp"])
        self.assertEqual(report["inventory"]["ipv4DefaultRoutes"][0]["interface"], "enp2s0f1")
        self.assertTrue(report["readyForNetworkChange"])

    def test_missing_evidence_and_failed_probes_block_cutover(self):
        with (
            patch.object(preflight, "json_command", return_value=(False, None)),
            patch.object(preflight, "command", return_value=(False, "")),
            patch.object(preflight, "address_candidates", return_value=None),
            patch.object(preflight, "profile_file_count", return_value="unknown"),
        ):
            report = preflight.collect("raspberrypi", Path("/tmp"), {})
        codes = {item["code"] for item in report["blockers"]}
        self.assertFalse(report["readyForNetworkChange"])
        self.assertIn("BELL_SETTINGS_SAVED", codes)
        self.assertIn("ROUTE_PROBE_UNAVAILABLE", codes)
        self.assertIn("LISTENER_PROBE_UNAVAILABLE", codes)
        self.assertIn("CONFIG_SCAN_UNAVAILABLE", codes)

    def test_listener_parser_distinguishes_ports(self):
        output = (
            "udp UNCONN 0 0 0.0.0.0:67 0.0.0.0:*\n"
            "tcp LISTEN 0 4096 [::]:53 [::]:*\n"
            "tcp LISTEN 0 4096 127.0.0.1:53800 0.0.0.0:*\n"
        )
        self.assertEqual(
            preflight.active_ports(output),
            {"dns": True, "dhcp": True, "technitium-management": False, "technitium-https": False},
        )
        self.assertEqual(
            preflight.listener_bindings(output),
            [
                {"service": "dhcp", "protocol": "udp", "binding": "wildcard"},
                {"service": "dns", "protocol": "tcp", "binding": "wildcard"},
            ],
        )

    def test_evidence_rejects_non_boolean_values(self):
        from tempfile import TemporaryDirectory

        with TemporaryDirectory() as directory:
            path = Path(directory) / "evidence.json"
            path.write_text('{"bell_settings_saved": "true"}', encoding="utf-8")
            with self.assertRaises(ValueError):
                preflight.load_evidence(path)


if __name__ == "__main__":
    unittest.main()
