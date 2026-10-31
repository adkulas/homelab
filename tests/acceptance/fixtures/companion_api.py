"""Disposable Sonarr/qBittorrent HTTP fixtures; no media or external indexers.

Real pinned companions consume these supported APIs. /fixture is test-only
control and records externally observable command, queue and blocklist state.
"""
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

lock = threading.RLock()
state = {"commands": [], "requests": [], "queued": False, "blocked": False,
         "torrent": True, "mode": "stalled", "hash": "a" * 40, "blocklistFailure": False,
         "wanted": True, "queuedEpisode": 103, "clientUnavailable": False, "idOffset": 0, "episodeCount": 14, "recentRelease": False}

def episode(number, **changes):
    result = {"id": 100 + number + state["idOffset"], "seriesId": 42, "seasonNumber": 1,
              "episodeNumber": number, "title": "Legal fixture episode",
              "monitored": True, "hasFile": False,
              "airDateUtc": "2000-01-01T00:00:00Z",
              "series": {"id": 42, "title": "Legal fixture", "monitored": True}}
    if state["recentRelease"] and number in (1, 2):
        result["airDateUtc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time()-3600)) if number == 1 else "2099-01-01T00:00:00Z"
    result.update(changes)
    return result

class API(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send(self, value, status=200):
        data = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        with lock:
            self.get()

    def get(self):
        parsed = urlparse(self.path)
        path = parsed.path
        state["requests"].append("GET " + path)
        if path.startswith("/api/v2/") and state["clientUnavailable"]:
            self.send({"error": "disposable client outage"}, 503)
            return
        records = [episode(n) for n in range(1, state["episodeCount"]+1)]
        records = [episode(90, monitored=False), episode(91, airDateUtc="2099-01-01T00:00:00Z"), episode(92, hasFile=True)] + records
        series = {"id": 42, "title": "Legal fixture", "monitored": True,
                  "tvdbId": 103354, "seasons": [{"seasonNumber": 1, "monitored": True}]}
        if path == "/fixture":
            self.send(state)
        elif path == "/api/v1/settings/sonarr":
            self.send([{"hostname": "sonarr", "port": 8989, "isDefault": True, "preventSearch": True}])
        elif path == "/api/v3/system/status":
            self.send({"appName": "Sonarr", "version": "4.0.17.2950"})
        elif path.startswith("/api/v3/wanted/"):
            wanted = [item for item in records if item["monitored"] and not item["hasFile"]] if path.endswith("missing") and state["wanted"] else []
            query = parse_qs(parsed.query)
            page, size = int(query.get("page", [1])[0]), int(query.get("pageSize", [40])[0])
            self.send({"page": page, "pageSize": size, "totalRecords": len(wanted),
                       "records": wanted[(page-1)*size:page*size]})
        elif path == "/api/v3/episode":
            self.send(records)
        elif path == "/api/v3/series":
            self.send([series])
        elif path == "/api/v3/series/42":
            self.send(series)
        elif path == "/api/v3/tag":
            self.send([])
        elif path == "/api/v3/queue/status":
            self.send({"totalCount": 2 if state["queued"] else (1 if state["queuedEpisode"] else 0), "count": 2 if state["queued"] else (1 if state["queuedEpisode"] else 0)})
        elif path in ("/api/v3/queue", "/api/v3/queue/details"):
            queue = []
            if state["queued"]:
                item = {"id": 7, "seriesId": 42, "episodeId": 101 + state["idOffset"], "seasonNumber": 1,
                        "series": series, "episode": episode(1), "title": "Legal.fixture.S01E01",
                        "downloadId": state["hash"], "downloadClient": "qBittorrent",
                        "protocol": "torrent", "size": 1000000, "sizeleft": 900000,
                        "status": "downloading", "trackedDownloadState": "downloading",
                        "trackedDownloadStatus": "ok", "statusMessages": [], "indexer": "Internet Archive"}
                if state["mode"] == "failed-import":
                    item.update(status="completed", trackedDownloadState="importPending",
                                trackedDownloadStatus="warning", sizeleft=0,
                                statusMessages=[{"title": "No video files were found", "messages": []}])
                if state["mode"] == "season":
                    queue = [dict(item, id=7+n, episodeId=101+n+state["idOffset"], episode=episode(1+n)) for n in range(2)]
                else:
                    queue = [item]
            if not state["queued"] and state["queuedEpisode"]:
                queue = [{"id": 99, "episodeId": state["queuedEpisode"], "seriesId": 42, "downloadId": "f"*40}]
            self.send(queue if path.endswith("/details") else {"records": queue, "totalRecords": len(queue), "page": 1, "pageSize": 100})
        elif path == "/api/v3/blocklist":
            records = [{"id": 1, "downloadId": state["hash"], "seriesId": 42}] if state["blocked"] else []
            self.send({"records": records, "totalRecords": len(records)})
        elif path == "/api/v3/command":
            self.send(state["commands"])
        elif path.startswith("/api/v3/command/"):
            self.send({"id": int(path.rsplit("/", 1)[1]), "status": "completed"})
        elif path == "/api/v2/app/version":
            self.send_text("v5.1.2")
        elif path == "/api/v2/app/webapiVersion":
            self.send_text("2.11.4")
        elif path == "/api/v2/transfer/speedLimitsMode":
            self.send_text("0")
        elif path == "/api/v2/app/preferences":
            self.send({"save_path": "/data/torrents", "current_network_interface": "tun0"})
        elif path == "/api/v2/torrents/info":
            torrents = []
            if state["queued"] and state["torrent"]:
                torrent = {"hash": state["hash"], "name": "Legal.fixture.S01E01", "category": "series",
                           "state": "stalledDL", "progress": 0.1, "downloaded": 100000,
                           "size": 1000000, "total_size": 1000000, "amount_left": 900000,
                           "dlspeed": 0, "upspeed": 0, "eta": 1000000, "ratio": 0,
                           "added_on": int(time.time())-7200, "last_activity": int(time.time())-7200,
                           "isPrivate": False, "availability": 0, "save_path": "/data/torrents/series"}
                if state["mode"] == "metadata":
                    torrent.update(state="metaDL", progress=0)
                elif state["mode"] == "slow-speed":
                    torrent.update(state="downloading", dlspeed=1024, eta=3600)
                elif state["mode"] == "slow-time":
                    torrent.update(state="downloading", dlspeed=204800, eta=172800)
                elif state["mode"] == "failed-import":
                    torrent.update(state="uploading", progress=1, amount_left=0)
                torrents = [torrent]
            self.send(torrents)
        elif path == "/api/v2/torrents/trackers":
            self.send([{"url": "http://fixture.invalid/announce", "status": 2, "num_peers": 0}])
        elif path == "/api/v2/torrents/properties":
            self.send({"is_private": False, "seeding_time": 0, "total_size": 1000000,
                       "creation_date": int(time.time())-7200})
        elif path == "/api/v2/torrents/files":
            self.send([{"index": 0, "name": "legal-episode.mp4", "size": 1000000, "priority": 1, "progress": 0.1}])
        elif path == "/api/v2/torrents/categories":
            self.send({"series": {"name": "series", "savePath": "/data/torrents/series"}})
        else:
            self.send({"unknown": path}, 404)

    def send_text(self, text):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(text.encode())

    def do_POST(self):
        with lock:
            parsed = urlparse(self.path)
            body = self.rfile.read(int(self.headers.get("Content-Length", "0"))).decode()
            state["requests"].append("POST " + parsed.path)
            if parsed.path == "/fixture":
                state.update(json.loads(body))
                self.send({"ok": True})
            elif parsed.path == "/api/v1/auth/jellyfin":
                self.send({"id": 1, "permissions": 2})
            elif parsed.path == "/api/v3/command":
                command = {key[0].lower()+key[1:]: value for key, value in json.loads(body).items()}
                entry = {"id": len(state["commands"])+1, "name": command["name"], "body": command,
                         "queued": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "status": "completed"}
                state["commands"].append(entry)
                self.send(entry, 201)
            elif parsed.path == "/api/v2/auth/login":
                self.send_response(200)
                self.send_header("Set-Cookie", "SID=fixture-session; Path=/")
                self.end_headers()
                self.wfile.write(b"Ok.")
            elif parsed.path == "/api/v2/torrents/delete":
                state["torrent"] = False
                self.send_text("")
            else:
                self.send({"unknown": parsed.path}, 404)

    def do_DELETE(self):
        with lock:
            parsed = urlparse(self.path)
            state["requests"].append("DELETE " + self.path)
            if not parsed.path.startswith("/api/v3/queue/"):
                self.send({}, 404)
            elif state["blocklistFailure"]:
                self.send({"error": "injected disposable blocklist failure"}, 500)
            else:
                query = parse_qs(parsed.query)
                state["blocked"] = query.get("blocklist") == ["true"]
                state["queued"] = False
                # Sonarr's supported removeFromClient operation forwards deletion.
                if query.get("removeFromClient") == ["true"]:
                    state["torrent"] = False
                self.send({})

for port in (8989, int(os.environ.get("QBIT_PORT", "8080"))):
    threading.Thread(target=ThreadingHTTPServer(("0.0.0.0", port), API).serve_forever, daemon=True).start()
threading.Event().wait()
