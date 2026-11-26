"""Exercise the Homepage transfer summary against a disposable qBittorrent API."""

from contextlib import contextmanager
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import runpy
import sys
from threading import Thread


class QbittorrentFixture(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/api/v2/sync/maindata?rid=0":
            self.send_error(404)
            return
        if self.headers.get("Authorization") != "Bearer fixture-key":
            self.send_error(401)
            return

        states = [
            "downloading", "forcedDL", "uploading", "forcedUP",
            "stoppedDL", "stoppedUP", "queuedDL", "queuedUP", "stalledDL", "stalledUP",
        ]
        body = json.dumps({
            "full_update": True,
            "server_state": {"dl_info_speed": 2_097_152, "up_info_speed": 1_048_576},
            "torrents": {str(index): {"state": state} for index, state in enumerate(states)},
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


@contextmanager
def serve(handler):
    with ThreadingHTTPServer(("127.0.0.1", 0), handler) as server:
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            yield server.server_port
        finally:
            server.shutdown()
            thread.join()


def read_summary(port):
    connection = HTTPConnection("127.0.0.1", port, timeout=10)
    try:
        connection.request("GET", "/transfer")
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


with serve(QbittorrentFixture) as upstream_port:
    os.environ["QBITTORRENT_HOST"] = "127.0.0.1"
    os.environ["QBITTORRENT_PORT"] = str(upstream_port)
    os.environ["QBITTORRENT_API_KEY"] = "fixture-key"
    adapter = runpy.run_path(sys.argv[1])["ActivityHandler"]
    with serve(adapter) as summary_port:
        status, body = read_summary(summary_port)
        assert status == 200, (status, body)
        assert json.loads(body) == {
            "downloadRate": 2_097_152, "uploadRate": 1_048_576, "downloading": 2, "seeding": 2,
        }, body

        os.environ["QBITTORRENT_API_KEY"] = "rejected-key"
        status, body = read_summary(summary_port)
        assert status == 502, (status, body)
        assert b"rejected-key" not in body, body
