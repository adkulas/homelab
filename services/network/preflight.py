#!/usr/bin/env python3
"""Read-only, redacted preflight for the home Site DNS and DHCP cutover."""

from __future__ import annotations

import argparse
import ipaddress
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


SITE_CHECKS = {
    "bell_settings_saved": "Save Bell LAN, gateway, DHCP pool, leases, DNS, IPv6, and port-forward settings in a private recovery packet.",
    "bell_dhcp_disable_confirmed": "Confirm the Bell DHCP switch and its re-enable path in the router admin page.",
    "planned_addresses_checked": "Compare every proposed static and reserved address with Bell leases and existing device settings.",
    "other_dhcp_absent": "Verify that no other DHCP responder serves the main LAN.",
    "ipv6_dns_audited": "Inspect Bell IPv6 RA/RDNSS and client DNS behavior; record any bypass path.",
    "hardcoded_addresses_reviewed": "Review hard-coded Host addresses in services, cameras, NVR, firewall, Tailscale, and port forwards.",
    "wired_recovery_client_tested": "Prove a wired client can reach Bell and both Hosts without relying on the new DNS service.",
    "bell_fallback_rehearsed": "Rehearse the order: disable Technitium DHCP, re-enable Bell DHCP, then renew a test client.",
}

HOST_CHECKS = {
    "homelab": {
        "host_profiles_saved": "Save the current Netplan files, Wi-Fi settings, routes, and rollback commands in the private packet.",
        "default_routes_reviewed": "Inspect all Ethernet and Wi-Fi default routes and their metrics before changing Netplan.",
        "local_console_ready": "Confirm local console access and a timed Netplan rollback before changing the Host address.",
        "port_listeners_reviewed": "Review any listeners on DNS, DHCP, or management ports before host-networked Technitium starts.",
    },
    "raspberrypi": {
        "host_profiles_saved": "Save the current NetworkManager profiles, routes, and rollback commands in the private packet.",
        "default_routes_reviewed": "Inspect all Ethernet and Wi-Fi default routes and their metrics before changing NetworkManager.",
        "local_console_ready": "Confirm local console access and a way to restore the prior NetworkManager profile.",
        "port_listeners_reviewed": "Review any listeners on DNS, DHCP, or management ports before Technitium starts.",
    },
    "recovery-client": {},
}

IPV4_LITERAL = re.compile(r"(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])")
MAC_LITERAL = re.compile(r"(?i)(?<![0-9a-f])(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}(?![0-9a-f])")
CONFIG_SUFFIXES = {".yaml", ".yml", ".toml", ".conf", ".env", ".service", ".timer", ".sh"}
PORTS = {53: "dns", 67: "dhcp", 5380: "technitium-management"}


def command(*args: str) -> tuple[bool, str]:
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=8, check=False)
    except (FileNotFoundError, subprocess.TimeoutExpired, OSError):
        return False, ""
    return result.returncode == 0, result.stdout if result.returncode == 0 else ""


def json_command(*args: str) -> tuple[bool, object]:
    ok, output = command(*args)
    if not ok:
        return False, None
    try:
        return True, json.loads(output)
    except json.JSONDecodeError:
        return False, None


def redacted_path(path: str) -> str:
    return MAC_LITERAL.sub("<mac>", IPV4_LITERAL.sub("<ipv4>", path))


def address_candidates(repo: Path) -> list[str] | None:
    ok, files = command("git", "-C", str(repo), "ls-files", "-z", "--", "sites/home", "services")
    if not ok:
        return None
    candidates = []
    for name in files.split("\0"):
        if not name or Path(name).suffix not in CONFIG_SUFFIXES or "secrets/" in name:
            continue
        try:
            contents = (repo / name).read_text(encoding="utf-8")
        except (OSError, UnicodeError):
            continue
        if any(relevant_private_ipv4(match.group()) for match in IPV4_LITERAL.finditer(contents)):
            candidates.append(redacted_path(name))
    return sorted(set(candidates))


def relevant_private_ipv4(value: str) -> bool:
    try:
        address = ipaddress.IPv4Address(value)
    except ipaddress.AddressValueError:
        return False
    return address.is_private and not (
        address.is_loopback or address.is_link_local or address.is_unspecified
    )


def active_ports(output: str) -> dict[str, bool]:
    occupied = {name: False for name in PORTS.values()}
    for line in output.splitlines():
        fields = line.split()
        if len(fields) < 5:
            continue
        protocol, endpoint = fields[0], fields[4]
        match = re.search(r":(\d+)$", endpoint)
        if not match:
            continue
        port = int(match.group(1))
        if port in PORTS and protocol.startswith(("tcp", "udp")):
            occupied[PORTS[port]] = True
    return occupied


def listener_bindings(output: str) -> list[dict[str, str]]:
    bindings = []
    for line in output.splitlines():
        fields = line.split()
        if len(fields) < 5:
            continue
        protocol, endpoint = fields[0], fields[4]
        match = re.search(r":(\d+)$", endpoint)
        if not match or int(match.group(1)) not in PORTS or not protocol.startswith(("tcp", "udp")):
            continue
        if endpoint.startswith(("127.", "[::1]")):
            binding = "loopback"
        elif endpoint.startswith(("0.0.0.0:", "[::]:", "*:")):
            binding = "wildcard"
        else:
            binding = "specific"
        bindings.append({"service": PORTS[int(match.group(1))], "protocol": protocol, "binding": binding})
    return sorted(bindings, key=lambda item: (item["service"], item["protocol"], item["binding"]))


def ipv6_resolver_present(output: str) -> bool:
    for token in output.replace("(", " ").replace(")", " ").split():
        try:
            if isinstance(ipaddress.ip_address(token.split("%", 1)[0]), ipaddress.IPv6Address):
                return True
        except ValueError:
            continue
    return False


def load_evidence(path: Path | None) -> dict[str, bool]:
    if path is None:
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("cannot read valid JSON evidence") from error
    if not isinstance(data, dict) or any(
        not isinstance(key, str) or type(value) is not bool for key, value in data.items()
    ):
        raise ValueError("evidence must be an object of boolean confirmations")
    return data


def collect(role: str, repo: Path, evidence: dict[str, bool]) -> dict[str, object]:
    blockers: list[dict[str, str]] = []
    inventory: dict[str, object] = {}
    for key, remedy in {**SITE_CHECKS, **HOST_CHECKS[role]}.items():
        if evidence.get(key) is not True:
            blockers.append({"code": key.upper(), "remedy": remedy})

    ok, routes = json_command("ip", "-j", "-4", "route", "show", "default")
    if not ok or not isinstance(routes, list):
        inventory["ipv4DefaultRoute"] = "unknown"
        blockers.append({"code": "ROUTE_PROBE_UNAVAILABLE", "remedy": "Run this check on the selected Host with iproute2 available."})
    else:
        inventory["ipv4DefaultRoute"] = "present" if routes else "absent"
        inventory["ipv4DefaultRouteCount"] = len(routes)
        inventory["ipv4DefaultRoutes"] = [
            {
                "interface": redacted_path(str(route.get("dev", "unknown"))),
                "metric": route.get("metric", "default"),
                "protocol": route.get("protocol", "unknown"),
            }
            for route in routes
            if isinstance(route, dict)
        ]
        if not routes:
            blockers.append({"code": "NO_IPV4_DEFAULT_ROUTE", "remedy": "Record and repair the current Host gateway before any cutover."})

    ok, addresses = json_command("ip", "-j", "-4", "address", "show")
    if not ok or not isinstance(addresses, list):
        inventory["nonLoopbackIpv4Address"] = "unknown"
        blockers.append({"code": "ADDRESS_PROBE_UNAVAILABLE", "remedy": "Run this check on the selected Host with iproute2 available."})
    else:
        has_address = any(
            interface.get("ifname") != "lo"
            and any(info.get("scope") == "global" for info in interface.get("addr_info", []))
            for interface in addresses
            if isinstance(interface, dict)
        )
        inventory["nonLoopbackIpv4Address"] = "present" if has_address else "absent"
        if not has_address:
            blockers.append({"code": "NO_NON_LOOPBACK_IPV4", "remedy": "Confirm the selected Host has a usable LAN address."})

    ok, sockets = command("ss", "-H", "-lntu")
    inventory["relevantListeners"] = active_ports(sockets) if ok else "unknown"
    inventory["listenerBindings"] = listener_bindings(sockets) if ok else "unknown"
    if not ok:
        blockers.append({"code": "LISTENER_PROBE_UNAVAILABLE", "remedy": "Install or run ss before reviewing port conflicts."})

    ok, resolvers = command("resolvectl", "dns")
    inventory["ipv6ResolverObserved"] = ipv6_resolver_present(resolvers) if ok else "unknown"
    inventory["ipv6ResolverProbe"] = "available" if ok else "unavailable; manual audit required"
    candidates = address_candidates(repo)
    inventory["hardcodedAddressCandidateFiles"] = candidates if candidates is not None else "unknown"
    if candidates is None:
        blockers.append({"code": "CONFIG_SCAN_UNAVAILABLE", "remedy": "Run from a Site repository checkout and review service address references manually."})
    inventory["hostProfileFileCount"] = profile_file_count(role)

    return {
        "kind": "SiteNetworkPreflight",
        "site": "home",
        "role": role,
        "checkedAt": datetime.now(timezone.utc).isoformat(),
        "readOnly": True,
        "inventory": inventory,
        "blockers": blockers,
        "readyForNetworkChange": not blockers,
    }


def profile_file_count(role: str) -> int | str:
    directory = Path("/etc/netplan") if role == "homelab" else Path("/etc/NetworkManager/system-connections")
    if role == "recovery-client":
        return "not applicable"
    try:
        return sum(1 for entry in directory.iterdir() if entry.is_file())
    except OSError:
        return "unknown"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--role", choices=HOST_CHECKS, required=True, help="Run on this actual Host role")
    parser.add_argument("--evidence", type=Path, help="Optional private JSON of confirmed operator checks")
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2], help=argparse.SUPPRESS)
    args = parser.parse_args()
    try:
        evidence = load_evidence(args.evidence)
    except ValueError as error:
        print(f"preflight: {error}", file=sys.stderr)
        return 64
    report = collect(args.role, args.repo, evidence)
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0 if report["readyForNetworkChange"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
