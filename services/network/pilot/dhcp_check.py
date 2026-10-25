#!/usr/bin/env python3
"""Exercise disposable DHCP allocation only on the technitium-pilot Docker bridge."""

import ipaddress
import json
import os
import subprocess
import sys
import time
import urllib.parse
import urllib.request

from pilot_support import COMPOSE, NETWORK, inspect_pilot, output

SCOPE = "pilot-home"
CLIENT_IMAGE = "docker.io/library/busybox:1.36.1@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
PROBE_IMAGE = "docker.io/library/python:3.13-alpine@sha256:bf12ecc0bea3f171d55145dd02c78b5eeb09f30ee2ac9cc2b4c9e4e8f5e30741"


def run_client(mac, hostname):
    script = """cat > /tmp/lease.sh <<'EOF'
#!/bin/sh
if [ "$1" = bound ]; then
  printf 'PILOT_LEASE|%s|%s|%s|%s|%s\\n' "$ip" "$router" "$dns" "$domain" "$search"
fi
EOF
chmod +x /tmp/lease.sh
ip addr flush dev eth0
udhcpc -i eth0 -f -q -n -t 4 -T 3 -O 119 -x hostname:__HOSTNAME__ -s /tmp/lease.sh
"""
    script = script.replace("__HOSTNAME__", hostname)
    result = subprocess.run(
        ["docker", "run", "--rm", "--network", NETWORK, "--cap-add", "NET_ADMIN",
         "--mac-address", mac, CLIENT_IMAGE, "sh", "-c", script],
        capture_output=True, text=True, timeout=40, check=False,
    )
    leases = [line.split("|", 5) for line in result.stdout.splitlines()
              if line.startswith("PILOT_LEASE|")]
    if result.returncode or len(leases) != 1:
        raise RuntimeError("isolated DHCP client did not receive exactly one lease")
    return leases[0][1:]


def count_offer_sources():
    probe = os.path.abspath("services/network/pilot/dhcp_offer_probe.py")
    result = subprocess.run(
        ["docker", "run", "--rm", "--network", NETWORK,
         "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW",
         "--mac-address", "02:54:00:00:00:32", "--volume", f"{probe}:/probe.py:ro",
         PROBE_IMAGE, "python3", "/probe.py"],
        capture_output=True, text=True, timeout=45, check=False,
    )
    if result.returncode or "PILOT_OFFERS=1" not in result.stdout.splitlines():
        raise RuntimeError("isolated client did not observe exactly one DHCP offer source")


def main():
    password_path = os.environ.get("PILOT_PASSWORD_FILE")
    if not password_path:
        raise RuntimeError("PILOT_PASSWORD_FILE is required")
    addresses, network = inspect_pilot()
    primary = addresses["primary"]
    secondary = addresses["secondary"]
    subnet = ipaddress.ip_network(network["IPAM"]["Config"][0]["Subnet"])
    gateway = network["IPAM"]["Config"][0]["Gateway"]
    if subnet.version != 4 or subnet.num_addresses < 256:
        raise RuntimeError("pilot bridge subnet is unsuitable for the test scope")
    address = lambda offset: str(subnet.network_address + offset)
    base = f"http://{primary}:5380/api/"
    cluster_response = output("dig", "+time=3", "+tries=1", "+norecurse",
                              "+noall", "+comments", f"@{secondary}", "home", "SOA")
    cluster_ready = "status: NOERROR" in cluster_response and " aa " in cluster_response
    if not cluster_ready:
        raise RuntimeError("secondary home zone is unavailable; run the cluster pilot first")
    token = None

    def api(path, values):
        body = urllib.parse.urlencode(values).encode()
        headers = {"Content-Type": "application/x-www-form-urlencoded"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        request = urllib.request.Request(base + path, data=body, headers=headers)
        with urllib.request.urlopen(request, timeout=15) as response:
            result = json.load(response)
        if result.get("status") != "ok":
            raise RuntimeError(f"Technitium API {path} failed: {result.get('status')}")
        return result

    with open(password_path, encoding="utf-8") as password_file:
        password = password_file.read().strip()
    deadline = time.monotonic() + 60
    while True:
        try:
            with urllib.request.urlopen(base + "status", timeout=3) as response:
                if json.load(response).get("status") == "ok":
                    break
        except OSError:
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError("pilot API did not become ready")
        time.sleep(2)
    token = api("user/login", {"user": "admin", "pass": password}).get("token")
    if not token:
        raise RuntimeError("Technitium login did not return a token")
    scopes = api("dhcp/scopes/list", {})["response"]["scopes"]
    if any(scope["name"] == SCOPE for scope in scopes):
        raise RuntimeError("pilot scope already exists; start with fresh pilot volumes")

    values = {
        "name": SCOPE,
        "startingAddress": address(20), "endingAddress": address(120),
        "subnetMask": str(subnet.netmask), "routerAddress": gateway,
        "leaseTimeDays": 0, "leaseTimeHours": 0, "leaseTimeMinutes": 1,
        "dnsServers": f"{primary},{secondary}", "useThisDnsServer": "false",
        "domainName": "home", "domainSearchList": "home", "dnsUpdates": "true",
        "dnsOverwriteForDynamicLease": "false", "blockLocallyAdministeredMacAddresses": "false",
        "exclusions": f"{address(20)}|{address(99)}",
        "reservedLeases": f"pilot-reserved|02-52-00-00-00-30|{address(30)}|pilot",
    }
    created = False
    try:
        api("dhcp/scopes/set", values)
        created = True
        api("dhcp/scopes/disable", {"name": SCOPE})
        scope = api("dhcp/scopes/get", {"name": SCOPE})["response"]
        if (scope["useThisDnsServer"] or scope["dnsServers"] != [primary, secondary]
                or scope["domainName"] != "home" or len(scope["exclusions"]) != 1
                or len(scope["reservedLeases"]) != 1):
            raise RuntimeError("Technitium did not retain the intended test options")
        print("pilot scope: reservation exclusion, two DNS endpoints, and home suffix retained")
        api("dhcp/scopes/enable", {"name": SCOPE})
        for kind, mac, expected, hostname in [
            ("reserved", "02:52:00:00:00:30", address(30), "pilot-reserved"),
            ("dynamic", "02:53:00:00:00:31", address(100), "pilot-dynamic"),
        ]:
            lease_ip, lease_router, lease_dns, lease_domain, lease_search = run_client(mac, hostname)
            if (lease_ip != expected or lease_router != gateway
                    or lease_dns.split() != [primary, secondary]
                    or lease_domain != "home" or lease_search != "home"):
                raise RuntimeError(f"{kind} DHCP offer did not match pilot topology")
            print(f"{kind} client: intended address, gateway, two DNS endpoints, home suffix")
            forward = output("dig", "+time=3", "+tries=1", "+short", f"@{primary}",
                             f"{hostname}.home", "A")
            reverse = output("dig", "+time=3", "+tries=1", "+short", f"@{primary}",
                             ipaddress.ip_address(expected).reverse_pointer, "PTR")
            if forward != expected or reverse.rstrip(".") != f"{hostname}.home":
                raise RuntimeError(f"{kind} lease did not create expected A/PTR records")
            print(f"{kind} client: A/PTR records created")
            if cluster_ready:
                for attempt in range(45):
                    secondary_forward = output("dig", "+time=3", "+tries=1", "+short",
                                               f"@{secondary}", f"{hostname}.home", "A")
                    secondary_reverse = output("dig", "+time=3", "+tries=1", "+short",
                                               f"@{secondary}",
                                               ipaddress.ip_address(expected).reverse_pointer, "PTR")
                    if (secondary_forward == expected
                            and secondary_reverse.rstrip(".") == f"{hostname}.home"):
                        break
                    time.sleep(2)
                else:
                    raise RuntimeError(f"{kind} lease A/PTR did not reach the secondary")
                print(f"{kind} client: A/PTR records served by secondary")
        api("dhcp/leases/remove", {
            "name": SCOPE, "hardwareAddress": "02:53:00:00:00:31",
        })
        dynamic_reverse = ipaddress.ip_address(address(100)).reverse_pointer
        for attempt in range(45):
            resolvers = [primary, secondary] if cluster_ready else [primary]
            if all(
                not output("dig", "+time=3", "+tries=1", "+short", f"@{resolver}",
                           "pilot-dynamic.home", "A")
                and not output("dig", "+time=3", "+tries=1", "+short", f"@{resolver}",
                               dynamic_reverse, "PTR")
                for resolver in resolvers
            ):
                break
            time.sleep(2)
        else:
            raise RuntimeError("removed dynamic lease left A/PTR records serving")
        print("dynamic client: lease removal cleaned A/PTR on configured resolvers")
        count_offer_sources()
        print("isolated client: exactly one DHCP offer source observed")
        leases = api("dhcp/leases/list", {})["response"]["leases"]
        offer_lease = next((lease for lease in leases if
                            lease["hardwareAddress"].replace("-", "").lower() == "025400000032"), None)
        if offer_lease is None:
            raise RuntimeError("offer-count client lease was not recorded")
        offer_address = offer_lease["address"]
        offer_reverse = ipaddress.ip_address(offer_address).reverse_pointer
        for attempt in range(36):
            active = api("dhcp/leases/list", {})["response"]["leases"]
            lease_present = any(lease["hardwareAddress"].replace("-", "").lower()
                                == "025400000032" for lease in active)
            resolvers = [primary, secondary] if cluster_ready else [primary]
            records_present = any(
                output("dig", "+time=3", "+tries=1", "+short", f"@{resolver}",
                       "pilot-offer.home", "A")
                or output("dig", "+time=3", "+tries=1", "+short", f"@{resolver}",
                          offer_reverse, "PTR")
                for resolver in resolvers
            )
            if not lease_present and not records_present:
                break
            time.sleep(5)
        else:
            raise RuntimeError("one-minute lease expiry left a lease or A/PTR record serving")
        print("dynamic client: one-minute lease expiry cleaned A/PTR on configured resolvers")
    finally:
        if created:
            try:
                api("dhcp/scopes/disable", {"name": SCOPE})
                api("dhcp/scopes/delete", {"name": SCOPE})
            except Exception:
                subprocess.run(COMPOSE + ["stop", "primary"], check=False, capture_output=True)
                raise


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError, RuntimeError) as error:
        print(f"pilot DHCP check failed: {error}", file=sys.stderr)
        sys.exit(1)
