#!/usr/bin/env python3
"""Count DHCP OFFER source identifiers from a client on the pilot bridge."""

import socket
import struct
import subprocess
import threading
import time

sources = set()
stop = threading.Event()
ready = threading.Event()


def capture():
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(0x0800)) as packets:
        packets.bind(("eth0", 0))
        packets.settimeout(0.5)
        ready.set()
        while not stop.is_set():
            try:
                frame = packets.recv(2048)
            except socket.timeout:
                continue
            if len(frame) < 14 + 20 or frame[12:14] != b"\x08\x00":
                continue
            ip = frame[14:]
            if ip[9] != 17:
                continue
            udp = ip[(ip[0] & 0x0F) * 4:]
            if len(udp) < 8 or struct.unpack("!HH", udp[:4]) != (67, 68):
                continue
            bootp = udp[8:]
            if len(bootp) < 240 or bootp[0] != 2 or bootp[236:240] != b"\x63\x82\x53\x63":
                continue
            options = bootp[240:]
            message_type = None
            server_id = None
            offset = 0
            while offset < len(options):
                code = options[offset]
                offset += 1
                if code == 255:
                    break
                if code == 0:
                    continue
                if offset >= len(options):
                    break
                size = options[offset]
                offset += 1
                value = options[offset:offset + size]
                offset += size
                if code == 53 and value:
                    message_type = value[0]
                if code == 54 and len(value) == 4:
                    server_id = value
            if message_type == 2:
                sources.add(server_id or ip[12:16])


def main():
    subprocess.run(["ip", "addr", "flush", "dev", "eth0"], check=True, capture_output=True)
    listener = threading.Thread(target=capture, daemon=True)
    listener.start()
    try:
        if not ready.wait(timeout=3):
            raise RuntimeError("offer capture did not become ready")
        result = subprocess.run(
            ["udhcpc", "-i", "eth0", "-f", "-q", "-n", "-t", "4", "-T", "3",
             "-x", "hostname:pilot-offer", "-s", "/bin/true"],
            capture_output=True, text=True, timeout=30, check=False,
        )
        time.sleep(1)
    finally:
        stop.set()
        listener.join(timeout=2)
    if result.returncode:
        raise RuntimeError("offer-count client did not obtain a DHCP lease")
    print(f"PILOT_OFFERS={len(sources)}")


if __name__ == "__main__":
    main()
