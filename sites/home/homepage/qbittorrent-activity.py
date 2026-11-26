"""Expose only the transfer rates and active torrent counts used by Homepage."""

from http.client import HTTPConnection, HTTPException
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os


def read_activity():
    connection = HTTPConnection(
        os.environ["QBITTORRENT_HOST"], int(os.environ["QBITTORRENT_PORT"]), timeout=5
    )
    try:
        connection.request(
            "GET",
            "/api/v2/sync/maindata?rid=0",
            headers={"Authorization": f"Bearer {os.environ['QBITTORRENT_API_KEY']}"},
        )
        response = connection.getresponse()
        if response.status != 200:
            raise OSError("qBittorrent rejected the activity request")
        snapshot = json.load(response)
    finally:
        connection.close()

    if snapshot["full_update"] is not True:
        raise ValueError("qBittorrent did not return a full snapshot")

    states = [torrent["state"] for torrent in snapshot.get("torrents", {}).values()]
    return {
        "downloadRate": snapshot["server_state"]["dl_info_speed"],
        "uploadRate": snapshot["server_state"]["up_info_speed"],
        "downloading": sum(state in ("downloading", "forcedDL") for state in states),
        "seeding": sum(state in ("uploading", "forcedUP") for state in states),
    }


class ActivityHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/transfer":
            self.send_error(404)
            return
        try:
            activity = read_activity()
        except (HTTPException, OSError, ValueError, KeyError, TypeError, AttributeError):
            self.send_error(502, "qBittorrent activity unavailable")
            return

        body = json.dumps(activity).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        # Avoid writing request details to Docker logs.
        pass


if __name__ == "__main__":
    with ThreadingHTTPServer(("0.0.0.0", 9100), ActivityHandler) as server:
        server.serve_forever()
