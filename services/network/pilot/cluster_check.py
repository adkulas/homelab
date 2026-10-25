#!/usr/bin/env python3
"""Check disposable Technitium cluster replication on the pilot bridge."""

import json
import ipaddress
import os
import socket
import ssl
import subprocess
import sys
import time
import urllib.parse
import urllib.request

from pilot_support import COMPOSE, inspect_pilot, output


class Api:
    def __init__(self, address, password):
        self.address = address
        self.token = None
        self.password = password
        self.login()

    def call(self, path, values=None, timeout=30):
        values = values or {}
        headers = {"Content-Type": "application/x-www-form-urlencoded"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        request = urllib.request.Request(
            f"http://{self.address}:5380/api/{path}",
            data=urllib.parse.urlencode(values).encode(), headers=headers,
        )
        with urllib.request.urlopen(request, timeout=timeout) as response:
            result = json.load(response)
        if result.get("status") != "ok":
            detail = result.get("errorMessage", "") if path.startswith("zones/") else ""
            raise RuntimeError(f"Technitium API {path} failed: {result.get('status')} {detail}")
        return result.get("response", result)

    def login(self):
        self.token = self.call("user/login", {"user": "admin", "pass": self.password})["token"]


def wait_until(label, predicate, seconds=60):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            if predicate():
                return
        except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError):
            pass
        time.sleep(2)
    raise RuntimeError(f"timed out waiting for {label}")


def dns_answer(address, name):
    return output("dig", "+time=3", "+tries=1", "+short", f"@{address}", name, "A")


def authoritative_answer(address, name):
    response = output("dig", "+time=3", "+tries=1", "+norecurse", "+noall",
                      "+comments", "+answer", f"@{address}", name, "A")
    return "status: NOERROR" in response and " aa " in response and name in response


def main():
    password_path = os.environ.get("PILOT_PASSWORD_FILE")
    if not password_path:
        raise RuntimeError("PILOT_PASSWORD_FILE is required")
    addresses, network = inspect_pilot()
    primary_address = addresses["primary"]
    secondary_address = addresses["secondary"]
    with open(password_path, encoding="utf-8") as password_file:
        password = password_file.read().strip()
    for service, address in (("primary", primary_address), ("secondary", secondary_address)):
        def ready():
            with urllib.request.urlopen(f"http://{address}:5380/api/status", timeout=3) as response:
                return json.load(response).get("status") == "ok"
        wait_until(f"{service} API startup", ready, seconds=60)
    primary = Api(primary_address, password)
    secondary = Api(secondary_address, password)
    for address in (primary_address, secondary_address):
        with urllib.request.urlopen(
            f"https://{address}:53443/api/status",
            context=ssl._create_unverified_context(), timeout=10,
        ) as response:
            if json.load(response).get("status") != "ok":
                raise RuntimeError("pilot HTTPS endpoint is unavailable")
        with socket.create_connection((address, 53), timeout=5):
            pass
    primary.call("settings/set", {
        "minSoaRefresh": 10, "minSoaRetry": 10, "blockingType": "NxDomain",
    })
    primary.call("admin/cluster/init", {
        "clusterDomain": "cluster.pilot.test", "primaryNodeIpAddresses": primary_address,
    }, timeout=60)
    secondary.call("admin/cluster/initJoin", {
        "secondaryNodeIpAddresses": secondary_address,
        "primaryNodeUrl": "https://primary.cluster.pilot.test:53443/",
        "primaryNodeIpAddress": primary_address,
        "ignoreCertificateErrors": "true",
        "primaryNodeUsername": "admin", "primaryNodePassword": password,
    }, timeout=120)
    secondary.login()
    for node in (primary, secondary):
        state = node.call("admin/cluster/state")
        if not state["clusterInitialized"] or len(state["clusterNodes"]) != 2:
            raise RuntimeError("pilot cluster did not have two joined nodes")
    print("cluster: two nodes joined over bridge-only HTTPS")
    wait_until("cluster SOA timer settings", lambda: (
        secondary.call("settings/get")["minSoaRefresh"] == 10
        and secondary.call("settings/get")["minSoaRetry"] == 10), seconds=90)

    catalog_names = primary.call("zones/catalogs/list")["catalogZoneNames"]
    catalog = "cluster-catalog.cluster.pilot.test"
    if catalog not in catalog_names:
        raise RuntimeError("cluster catalog zone was not created")
    primary.call("zones/create", {"zone": "pilot.test", "type": "Primary", "catalog": catalog})
    records = primary.call("zones/records/get", {"domain": "pilot.test"})["records"]
    soa = next(record["rData"] for record in records if record["type"] == "SOA")
    primary.call("zones/records/update", {
        "domain": "pilot.test", "type": "SOA", "ttl": 30,
        "primaryNameServer": soa["primaryNameServer"],
        "responsiblePerson": soa["responsiblePerson"],
        "serial": soa["serial"] + 1, "refresh": 10, "retry": 10,
        "expire": 60, "minimum": 5, "useSerialDateScheme": "false",
    })
    primary.call("zones/records/add", {
        "domain": "service.pilot.test", "type": "A", "ipAddress": primary_address, "ttl": 30,
    })
    wait_until("catalog member zone replication", lambda: dns_answer(
        secondary_address, "service.pilot.test") == primary_address, seconds=90)
    secondary_soa = output("dig", "+time=3", "+tries=1", "+short",
                           f"@{secondary_address}", "pilot.test", "SOA").split()
    if len(secondary_soa) < 7 or int(secondary_soa[-2]) != 60:
        raise RuntimeError("secondary did not receive the 60-second test SOA expiry")
    print("catalog: primary A record served by secondary")
    primary.call("zones/create", {"zone": "home", "type": "Primary", "catalog": catalog})
    subnet = ipaddress.ip_network(network["IPAM"]["Config"][0]["Subnet"])
    reverse_zone = primary.call("zones/create", {
        "zone": str(subnet), "type": "Primary", "catalog": catalog,
    })["domain"]

    def secondary_has_dhcp_zones():
        names = {zone["name"] for zone in secondary.call("zones/list")["zones"]}
        return {"home", reverse_zone}.issubset(names)

    wait_until("home and reverse catalog zones", secondary_has_dhcp_zones, seconds=90)
    print("catalog: home and reverse zones provisioned on secondary")

    blocked_name = "example.com"
    if not all(dns_answer(address, blocked_name) for address in (primary_address, secondary_address)):
        raise RuntimeError("external filtering test domain did not resolve before blocking")
    primary.call("blocked/add", {"domain": blocked_name})

    def secondary_has_block():
        secondary.login()
        records = secondary.call("blocked/list", {"domain": blocked_name})["records"]
        return any(record.get("name") == blocked_name for record in records)

    wait_until("blocked-zone policy replication", secondary_has_block, seconds=90)
    wait_until("replicated filtering answers", lambda: all(
        "status: NXDOMAIN" in output("dig", "+time=3", "+tries=1", "+noall", "+comments",
                                     f"@{address}", blocked_name, "A")
        for address in (primary_address, secondary_address)), seconds=60)
    print("filtering: blocked-zone policy synchronized to secondary")

    output(*COMPOSE, "stop", "primary")
    try:
        if not authoritative_answer(secondary_address, "service.pilot.test"):
            raise RuntimeError("secondary stopped answering before the test SOA expiry")
        time.sleep(75)
        if authoritative_answer(secondary_address, "service.pilot.test"):
            raise RuntimeError("secondary continued authoritative answers past test SOA expiry")
        if not dns_answer(secondary_address, "iana.org"):
            raise RuntimeError("secondary DNS service failed while testing zone expiry")
        print("expiry: secondary stopped authoritative service after 60-second test SOA expiry")
    finally:
        output(*COMPOSE, "start", "primary")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError, RuntimeError) as error:
        print(f"pilot cluster check failed: {error}", file=sys.stderr)
        sys.exit(1)
