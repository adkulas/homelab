#!/usr/bin/env python3
"""Pilot a narrow bearer-token API fallback on a disposable Technitium instance."""

import atexit
import io
import ipaddress
import json
import os
import subprocess
import sys
import time
import urllib.parse
import urllib.request
import zipfile

from pilot_support import COMPOSE, inspect_pilot

ZONE = "automation.pilot.test"
RECORD = "service.automation.pilot.test"
SCOPE = "automation-pilot"


class Api:
    def __init__(self, address, password):
        self.base = f"http://{address}:5380/api/"
        self.token = None
        deadline = time.monotonic() + 60
        while True:
            try:
                with urllib.request.urlopen(self.base + "status", timeout=3) as response:
                    if json.load(response).get("status") == "ok":
                        break
            except OSError:
                pass
            if time.monotonic() >= deadline:
                raise RuntimeError("pilot API did not become ready")
            time.sleep(2)
        self.token = self.call("user/login", {"user": "admin", "pass": password})["token"]

    def request(self, path, values=None, content_type="application/x-www-form-urlencoded", body=None):
        if values is None:
            values = {}
        if body is None:
            body = urllib.parse.urlencode(values).encode()
        headers = {"Content-Type": content_type}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        request = urllib.request.Request(self.base + path, data=body, headers=headers)
        with urllib.request.urlopen(request, timeout=45) as response:
            return response.read()

    def call(self, path, values=None):
        result = json.loads(self.request(path, values))
        if result.get("status") != "ok":
            raise RuntimeError(f"Technitium API {path} failed: {result.get('status')}")
        return result.get("response", result)


def live_value(api):
    zones = api.call("zones/list", {"filterName": ZONE})["zones"]
    if not any(zone["name"] == ZONE for zone in zones):
        return None, None
    records = api.call("zones/records/get", {"domain": RECORD})["records"]
    addresses = [record["rData"]["ipAddress"] for record in records
                 if record["name"] == RECORD and record["type"] == "A"]
    if len(addresses) > 1:
        raise RuntimeError("pilot record has multiple A values")
    return True, addresses[0] if addresses else None


def plan(api, target):
    zone_exists, current = live_value(api)
    if zone_exists is None:
        return ["create zone", "add A"]
    if current is None:
        return ["add A"]
    if current != target:
        return ["update A"]
    return []


def scope_enabled(api, name):
    matches = [scope for scope in api.call("dhcp/scopes/list")["scopes"] if scope["name"] == name]
    if len(matches) != 1:
        raise RuntimeError("pilot scope is missing or duplicated")
    return matches[0]["enabled"]


def apply(api, target, actions):
    if "create zone" in actions:
        api.call("zones/create", {"zone": ZONE, "type": "Primary"})
    if "add A" in actions:
        api.call("zones/records/add", {"domain": RECORD, "type": "A", "ipAddress": target})
    if "update A" in actions:
        _, current = live_value(api)
        api.call("zones/records/update", {
            "domain": RECORD, "type": "A", "ipAddress": current,
            "newIpAddress": target,
        })
    if plan(api, target):
        raise RuntimeError("post-apply state differs from the plan")


def backup_components(api):
    query = urllib.parse.urlencode({"zones": "true", "scopes": "true"})
    url = api.base + "settings/backup?" + query
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {api.token}"})
    for attempt in range(6):
        with urllib.request.urlopen(request, timeout=45) as response:
            archive = response.read()
        with zipfile.ZipFile(io.BytesIO(archive)) as backup:
            if backup.testzip() is not None:
                raise RuntimeError("pilot backup ZIP failed integrity check")
            names = [name.lower() for name in backup.namelist()]
            if any("zone" in name for name in names) and any("scope" in name for name in names):
                return archive
        time.sleep(2)
    raise RuntimeError("pilot backup ZIP lacked flushed zone or scope files")


def restore_components(api, archive):
    boundary = "technitium-pilot-restore"
    body = (
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; "
        "filename=\"pilot.zip\"\r\nContent-Type: application/zip\r\n\r\n"
    ).encode() + archive + f"\r\n--{boundary}--\r\n".encode()
    result = json.loads(api.request(
        "settings/restore?zones=true&scopes=true",
        content_type=f"multipart/form-data; boundary={boundary}",
        body=body,
    ))
    if result.get("status") != "ok":
        raise RuntimeError("pilot zone restore failed")


def main():
    password_path = os.environ.get("PILOT_PASSWORD_FILE")
    if not password_path:
        raise RuntimeError("PILOT_PASSWORD_FILE is required")
    addresses, network = inspect_pilot()
    primary = addresses["primary"]
    secondary = addresses["secondary"]
    subnet = ipaddress.ip_network(network["IPAM"]["Config"][0]["Subnet"])
    address = lambda offset: str(subnet.network_address + offset)
    with open(password_path, encoding="utf-8") as password_file:
        api = Api(primary, password_file.read().strip())
    if any(scope["enabled"] for scope in api.call("dhcp/scopes/list")["scopes"]):
        raise RuntimeError("pilot primary has an enabled DHCP scope")
    first = plan(api, primary)
    if first != ["create zone", "add A"] or live_value(api) != (None, None):
        raise RuntimeError("dry run mutated the pilot server")
    print("read/diff and dry run: create zone and A record; no mutation")
    apply(api, primary, first)
    if plan(api, primary):
        raise RuntimeError("second apply would change an unchanged record")
    print("first apply verified; second plan is empty")
    update = plan(api, secondary)
    if update != ["update A"]:
        raise RuntimeError("changed address did not produce an update plan")
    apply(api, secondary, update)
    print("update planned, applied, and verified")
    if any(scope["name"] == SCOPE for scope in api.call("dhcp/scopes/list")["scopes"]):
        raise RuntimeError("pilot restore scope already exists")
    scope_values = {
        "name": SCOPE, "startingAddress": address(20), "endingAddress": address(120),
        "subnetMask": str(subnet.netmask), "routerAddress": network["IPAM"]["Config"][0]["Gateway"],
        "dnsServers": f"{primary},{secondary}", "useThisDnsServer": "false",
        "domainName": "home", "exclusions": f"{address(20)}|{address(99)}",
        "reservedLeases": f"pilot-restore|02-56-00-00-00-30|{address(30)}|pilot",
    }
    def fail_closed_scope():
        try:
            if any(scope["name"] == SCOPE for scope in api.call("dhcp/scopes/list")["scopes"]):
                api.call("dhcp/scopes/disable", {"name": SCOPE})
        except Exception:
            subprocess.run(COMPOSE + ["stop", "primary"], check=False, capture_output=True)

    atexit.register(fail_closed_scope)
    api.call("dhcp/scopes/set", scope_values)
    created_enabled = scope_enabled(api, SCOPE)
    if created_enabled:
        print("scope creation enabled allocation by default; disabling before backup")
        api.call("dhcp/scopes/disable", {"name": SCOPE})
    scope = api.call("dhcp/scopes/get", {"name": SCOPE})
    if (scope_enabled(api, SCOPE) or len(scope["reservedLeases"]) != 1
            or scope["reservedLeases"][0]["address"] != address(30)):
        raise RuntimeError("pilot restore scope did not retain disabled reservation")
    archive = backup_components(api)
    api.call("zones/delete", {"zone": ZONE})
    api.call("dhcp/scopes/delete", {"name": SCOPE})
    if live_value(api) != (None, None):
        raise RuntimeError("pilot zone remained after deletion")
    if any(scope["name"] == SCOPE for scope in api.call("dhcp/scopes/list")["scopes"]):
        raise RuntimeError("pilot scope remained after deletion")
    restore_components(api, archive)
    if live_value(api) != (True, secondary):
        raise RuntimeError("restored zone does not contain the updated A record")
    restored_scope = api.call("dhcp/scopes/get", {"name": SCOPE})
    if (scope_enabled(api, SCOPE) or len(restored_scope["reservedLeases"]) != 1
            or restored_scope["reservedLeases"][0]["address"] != address(30)
            or len(restored_scope["exclusions"]) != 1):
        raise RuntimeError("restored scope lost its disabled reservation or exclusion")
    if any(scope["enabled"] for scope in api.call("dhcp/scopes/list")["scopes"]):
        raise RuntimeError("restore unexpectedly enabled DHCP")
    print("zone/scope backup ZIP verified; isolated restore recovered record and disabled reservation")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError, RuntimeError,
            zipfile.BadZipFile) as error:
        print(f"pilot automation check failed: {error}", file=sys.stderr)
        sys.exit(1)
