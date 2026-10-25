#!/usr/bin/env python3
"""Measure native DNS and filtering inside the Pi's outbound-only namespace."""

from concurrent.futures import ThreadPoolExecutor
import json
import os
import secrets
import socket
import struct
import time
import urllib.parse
import urllib.request


def query(name):
    question = b"".join(bytes([len(label)]) + label.encode() for label in name.split(".")) + b"\0"
    packet = struct.pack("!HHHHHH", secrets.randbelow(65536), 0x0100, 1, 0, 0, 0)
    packet += question + struct.pack("!HH", 1, 1)
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
        client.settimeout(20)
        start = time.perf_counter()
        client.sendto(packet, ("127.0.0.1", 53))
        response, _ = client.recvfrom(1500)
        milliseconds = (time.perf_counter() - start) * 1000
    _, flags, questions, count, _, _ = struct.unpack("!HHHHHH", response[:12])
    if questions != 1:
        raise RuntimeError("unexpected DNS question count")
    offset = 12
    while response[offset]:
        offset += response[offset] + 1
    offset += 5
    addresses = []
    for _ in range(count):
        if response[offset] & 0xC0 == 0xC0:
            offset += 2
        else:
            while response[offset]:
                offset += response[offset] + 1
            offset += 1
        kind, _, _, size = struct.unpack("!HHIH", response[offset:offset + 10])
        offset += 10
        if kind == 1 and size == 4:
            addresses.append(socket.inet_ntoa(response[offset:offset + 4]))
        offset += size
    return flags & 15, addresses, milliseconds


def api(path, values, token=None):
    headers = {"Content-Type": "application/x-www-form-urlencoded"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(
        f"http://127.0.0.1:15380/api/{path}",
        data=urllib.parse.urlencode(values).encode(), headers=headers,
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        result = json.load(response)
    if result.get("status") != "ok":
        raise RuntimeError(f"Pi API {path} failed")
    return result


def main():
    first = query("example.com")
    if first[0] != 0 or not first[1]:
        raise RuntimeError("native Pi recursion did not answer an external A query")
    times = [first[2]]
    for _ in range(49):
        code, addresses, elapsed = query("example.com")
        if code != 0 or not addresses:
            raise RuntimeError("repeated native Pi DNS query failed")
        times.append(elapsed)
    with ThreadPoolExecutor(max_workers=8) as workers:
        concurrent = list(workers.map(lambda _: query("example.com"), range(800)))
    if any(code != 0 or not addresses for code, addresses, _ in concurrent):
        raise RuntimeError("concurrent native Pi DNS queries failed")
    concurrent_p95 = sorted(result[2] for result in concurrent)[759]
    with open(os.path.join(os.environ["PILOT_DIR"], "admin-password"), encoding="utf-8") as password_file:
        password = password_file.read().strip()
    token = api("user/login", {"user": "admin", "pass": password})["token"]
    api("settings/set", {"blockingType": "NxDomain"}, token)
    api("blocked/add", {"domain": "example.com"}, token)
    blocked_code, blocked_addresses, _ = query("example.com")
    if blocked_code != 3 or blocked_addresses:
        raise RuntimeError("native Pi filtering did not return the expected blocking answer")
    with ThreadPoolExecutor(max_workers=8) as workers:
        filtered = list(workers.map(lambda _: query("example.com"), range(800)))
    if any(code != 3 or addresses for code, addresses, _ in filtered):
        raise RuntimeError("concurrent native Pi filtering did not return the blocking answer")
    filtered_p95 = sorted(result[2] for result in filtered)[759]
    with open(f"/proc/{os.environ['PILOT_SERVER_PID']}/status", encoding="utf-8") as status_file:
        rss = next(line.split()[1] for line in status_file if line.startswith("VmRSS:"))
    print(f"Pi native recursion: cold_ms={times[0]:.1f} sequential_p95_ms={sorted(times)[47]:.1f} samples=50")
    print(f"Pi native cached DNS: workers=8 queries=800 p95_ms={concurrent_p95:.1f}")
    print(f"Pi native filtering: workers=8 queries=800 p95_ms={filtered_p95:.1f}")
    print(f"Pi native memory: rss_kib={rss}")


if __name__ == "__main__":
    main()
