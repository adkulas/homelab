#!/usr/bin/env python3
"""Rehearse trusted management HTTPS and leaf replacement on disposable DNS Hosts."""

import os
from pathlib import Path
import subprocess
import sys
import time

from automation_check import Api
from pilot_support import COMPOSE, inspect_pilot, output

CLI = Path("services/network/ca/certificates.py").resolve()


def cli(*args, ok=True):
    result = subprocess.run([sys.executable, str(CLI), *map(str, args)], capture_output=True)
    if (result.returncode == 0) != ok:
        raise RuntimeError("certificate operation returned an unexpected result")


def main():
    workspace = Path(sys.argv[1])
    password = Path(os.environ["PILOT_PASSWORD_FILE"])
    addresses, _ = inspect_pilot()
    cli("root", "--output", workspace / "ca", "--password-file", password)
    root = workspace / "ca/root.crt"
    for service, name, suffix in (("primary", "homelab.home", "first"),
                                  ("secondary", "raspberrypi.home", "first"),
                                  ("secondary", "raspberrypi.home", "renewed")):
        api = Api(addresses[service], password.read_text().strip())
        if any(scope["enabled"] for scope in api.call("dhcp/scopes/list")["scopes"]):
            raise RuntimeError("HTTPS rehearsal requires disabled DHCP scopes")
        request = workspace / (service + "-request-" + suffix)
        leaf = workspace / (service + "-leaf-" + suffix)
        pfx = workspace / (service + "-" + suffix + ".pfx")
        cli("request", "--name", name, "--output", request)
        cli("sign", "--name", name, "--ca", workspace / "ca", "--request", request / "leaf.csr",
            "--output", leaf, "--password-file", password)
        cli("bundle", "--name", name, "--key", request / "leaf.key", "--cert", leaf / "leaf.crt",
            "--ca-cert", root, "--password-file", password, "--output", pfx)
        container = output(*COMPOSE, "ps", "-q", service)
        certificate_path = "/etc/dns/" + pfx.name
        subprocess.run(["docker", "cp", str(pfx), container + ":" + certificate_path],
                       check=True, capture_output=True)
        api.call("settings/set", {
            "webServiceEnableTls": "true", "webServiceUseSelfSignedTlsCertificate": "false",
            "webServiceTlsCertificatePath": certificate_path,
            "webServiceTlsCertificatePassword": password.read_text().strip(),
        })
        common = ("--name", name, "--address", addresses[service], "--ca-cert", root, "--expected-cert", leaf / "leaf.crt")
        deadline = time.monotonic() + 45
        while True:
            result = subprocess.run([sys.executable, str(CLI), "probe", *map(str, common)],
                                    capture_output=True)
            if result.returncode == 0:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("trusted Technitium HTTPS did not become ready")
            time.sleep(1)
        cli("probe", "--name", "wrong.home", "--address", addresses[service], "--ca-cert", root, ok=False)
        cli("probe", *common, "--min-days", 366, ok=False)
        if any(scope["enabled"] for scope in api.call("dhcp/scopes/list")["scopes"]):
            raise RuntimeError("HTTPS configuration unexpectedly enabled DHCP")
        print(f"{service} {suffix}: trusted name and chain, expiry guard; DHCP disabled")
    print("HTTPS rehearsal passed: both DNS Hosts and supervised secondary renewal")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        detail = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print("HTTPS rehearsal failed: " + detail, file=sys.stderr)
        sys.exit(1)
