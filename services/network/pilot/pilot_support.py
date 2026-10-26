"""Shared boundary checks for the disposable Docker pilot."""

import json
import subprocess

COMPOSE = ["docker", "compose", "--env-file", "services/network/technitium.env", "-f", "services/network/pilot/compose.yaml"]
NETWORK = "technitium-pilot_pilot"


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def inspect_pilot():
    network = json.loads(output("docker", "network", "inspect", NETWORK))[0]
    if network["Driver"] != "bridge" or len(network["Containers"]) != 2:
        raise RuntimeError("pilot bridge has an unexpected driver or attached container")
    addresses = {}
    container_ids = set()
    for service in ("primary", "secondary"):
        container_id = output(*COMPOSE, "ps", "-q", service)
        if not container_id:
            raise RuntimeError(f"{service}: pilot container is absent")
        details = json.loads(output("docker", "inspect", container_id))[0]
        labels = details["Config"]["Labels"]
        if (labels.get("com.docker.compose.project") != "technitium-pilot"
                or labels.get("com.docker.compose.service") != service):
            raise RuntimeError(f"{service}: unexpected Compose identity")
        if not details["State"]["Running"]:
            raise RuntimeError(f"{service}: pilot container is not running")
        if set(details["NetworkSettings"]["Networks"]) != {NETWORK}:
            raise RuntimeError(f"{service}: not isolated on the pilot bridge")
        if any(details["NetworkSettings"]["Ports"].values()):
            raise RuntimeError(f"{service}: published Host port found")
        addresses[service] = details["NetworkSettings"]["Networks"][NETWORK]["IPAddress"]
        container_ids.add(details["Id"])
    if set(network["Containers"]) != container_ids:
        raise RuntimeError("pilot bridge contains an unexpected container")
    return addresses, network
