#!/usr/bin/env python3
"""Stateful Docker boundary fixture for the public destroy/lifecycle tests."""
import io
import json
import os
import socket
import sys
import tarfile
import time
from pathlib import Path

path = Path(os.environ["DESTROY_FIXTURE"])
state = json.loads(path.read_text())
args = sys.argv[1:]
op = " ".join(args[:2]) if args[0] in ("container", "volume", "network", "context") else args[0]
state.setdefault("calls", []).append(op)

def save():
    path.write_text(json.dumps(state))

def emit(value):
    print(json.dumps(value))

save()
failure_op = "helper rm" if op == "rm" and args[-1] in state.get("helpers", {}) else op
if state.get("fail") == failure_op:
    sys.exit(71)
if state.get("delay") == op:
    time.sleep(20)
if op == "context inspect":
    emit(state.get("endpoint", "unix:///var/run/docker.sock"))
elif op == "info":
    emit({"ID": state.get("daemon", "fixture-daemon"), "Name": socket.gethostname()})
elif op == "ps":
    print("\n".join(state["containers"]))
elif op == "container inspect":
    emit([state["containers"][name] for name in args[2:]])
elif op == "volume ls":
    print("\n".join(state["volumes"]))
elif op == "volume inspect":
    name = args[2]
    if name not in state["volumes"]:
        sys.exit(1)
    emit([state["volumes"][name]])
elif op == "network ls":
    project = next(a.split("=", 2)[2] for a in args if a.startswith("label="))
    print("\n".join(n["Id"] for n in state["networks"].values() if n["Labels"]["com.docker.compose.project"] == project))
elif op == "network inspect":
    network = dict(state["networks"][args[2]])
    network["Containers"] = {cid: {} for cid, c in state["containers"].items() if c.get("network") == args[2]}
    emit([network])
elif op == "stop" or op == "start":
    state["containers"][args[1]]["State"]["Running"] = op == "start"
    save()
elif op == "rm":
    name = args[-1]
    state["containers"].pop(name, None)
    state.setdefault("helpers", {}).pop(name, None)
    save()
elif op == "network rm":
    del state["networks"][args[2]]
    save()
elif op == "volume rm":
    del state["volumes"][args[2]]
    save()
elif op == "create":
    mount = args[args.index("--mount") + 1]
    source = mount.split("source=", 1)[1].split(",", 1)[0]
    if not Path(source).is_dir():
        sys.exit(1)
    name = "helper-" + str(len(state["calls"]))
    state.setdefault("helpers", {})[name] = source
    label = args[args.index("--label") + 1].split("=", 1)
    state["containers"][name] = {"Id": name, "Config": {"Labels": {label[0]: label[1]}}, "State": {"Running": False}, "Mounts": [{"Type": "bind", "Source": source}]}
    save()
    print(name)
elif op == "cp":
    if state.get("edit_config_on_cp"):
        with open(state["edit_config_on_cp"], "a") as declaration:
            declaration.write("\n# changed during final-state capture\n")
    name = args[-2].split(":", 1)[0]
    if state.get("corrupt_second_stream") and state["calls"].count("cp") == 2:
        sys.stdout.buffer.write(b"invalid tar stream")
    else:
        with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as archive:
            archive.add(state["helpers"][name], arcname=".")
else:
    sys.exit(93)
