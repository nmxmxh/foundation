#!/usr/bin/env python3
"""Validate rendered Core Nginx templates against an instrumented HTTP origin."""
import collections
import concurrent.futures
import http.client
import http.server
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parent.parent


def command(*args, timeout=60):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=timeout).strip()


class Origin(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    counts = collections.Counter()
    lock = threading.Lock()

    def do_GET(self):
        with self.lock:
            self.counts[self.path] += 1
            count = self.counts[self.path]
        if self.headers.get("Upgrade") == "websocket":
            self.send_response(101)
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.send_header("X-Origin-Encoding", self.headers.get("Accept-Encoding", "absent"))
            self.end_headers()
            self.close_connection = True
            return
        if "slow" in self.path:
            time.sleep(0.05)
        payload = str(count).encode()
        self.send_response(503 if "failure" in self.path else 200)
        self.send_header("Content-Type", "application/xml" if ".xml" in self.path else "text/plain")
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("X-Origin-Count", str(count))
        self.send_header("X-Origin-Connection", str(self.client_address[1]))
        self.send_header("X-Origin-Host", self.headers.get("Host", "absent"))
        if "private" in self.path:
            self.send_header("Cache-Control", "private, no-store")
        if "cookie" in self.path:
            self.send_header("Set-Cookie", "session=fixture")
        if "vary" in self.path:
            self.send_header("Vary", "X-Fixture-Variant")
            self.send_header("X-Origin-Variant", self.headers.get("X-Fixture-Variant", "none"))
        if "expired" in self.path:
            self.send_header("X-Accel-Expires", "1")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(payload)

    do_HEAD = do_GET
    do_POST = do_GET

    def log_message(self, *_):
        pass


def main():
    if "--origin" in sys.argv:
        http.server.ThreadingHTTPServer(("0.0.0.0", 8080), Origin).serve_forever()
        return
    try:
        command("docker", "info", "--format", "{{.ServerVersion}}", timeout=10)
    except (OSError, subprocess.SubprocessError):
        if os.environ.get("CI") or os.environ.get("FOUNDATION_REQUIRE_EDGE_TESTS") == "1":
            raise RuntimeError("Nginx integration requires a running Docker daemon") from None
        print("SKIP: Nginx integration requires Docker; CI requires this lane")
        return
    name = "foundation-edge-" + uuid.uuid4().hex[:10]
    edge, origin = name + "-edge", name + "-origin"
    try:
        command("docker", "network", "create", name)
        (ROOT / ".cache").mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=name, dir=ROOT / ".cache") as directory:
            work = pathlib.Path(directory)
            conf = work / "conf"
            conf.mkdir()
            html = work / "html"
            html.mkdir()
            (html / "index.html").write_text("fixture-spa")
            (html / "asset.js").write_text("fixture-static")
            global_config = (ROOT / "templates/config/nginx.conf").read_text().replace("worker_processes auto;", "worker_processes 1;")
            (work / "nginx.conf").write_text(global_config)
            server = (ROOT / "templates/config/default.conf.template").read_text()
            for key, value in {"UPSTREAM_HOST": "origin", "UPSTREAM_PORT": "8080", "UPSTREAM_KEEPALIVE_CONNECTIONS": "8", "UPSTREAM_KEEPALIVE_REQUESTS": "100", "UPSTREAM_KEEPALIVE_TIMEOUT": "15s"}.items():
                server = server.replace("${" + key + "}", value)
            server = server.replace("    charset utf-8;", "    charset utf-8;\n    include /etc/nginx/conf.d/public-cache.server.inc;\n    add_header X-Fixture-Cache $upstream_cache_status always;")
            server += "\n"
            (conf / "default.conf").write_text(server)
            public_http = (ROOT / "templates/config/public-cache.http.conf.example").read_text()
            public_server = (ROOT / "templates/config/public-cache.server.inc.example").read_text()
            public_server += "\nlocation ^~ /public/ { proxy_pass http://foundation_backend; proxy_http_version 1.1; proxy_set_header Connection \"\"; }\n"
            (conf / "public-cache.conf").write_text(public_http)
            (conf / "public-cache.server.inc").write_text(public_server)
            command("docker", "run", "-d", "--name", origin, "--network", name, "--network-alias", "origin", "-v", f"{pathlib.Path(__file__).resolve()}:/fixture.py:ro", "python:3.14-alpine", "python", "/fixture.py", "--origin", timeout=120)
            command("docker", "run", "-d", "--name", edge, "--network", name, "-p", "127.0.0.1::80", "-v", f"{work / 'nginx.conf'}:/etc/nginx/nginx.conf:ro", "-v", f"{conf}:/etc/nginx/conf.d:ro", "-v", f"{html}:/usr/share/nginx/html:ro", "alpine:3.23", "sh", "-ec", "apk add --no-cache nginx nginx-mod-http-brotli; mkdir -p /var/cache/nginx; nginx -t; exec nginx -g 'daemon off;'", timeout=120)
            mapping = json.loads(command("docker", "inspect", edge))[0]["NetworkSettings"]["Ports"]["80/tcp"][0]
            port = int(mapping["HostPort"])

            def request(path, headers=None, method="GET"):
                connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
                try:
                    connection.request(method, path, headers=headers or {})
                    response = connection.getresponse()
                    return response.status, dict(response.getheaders()), response.read()
                finally:
                    connection.close()

            for _ in range(120):
                try:
                    if request("/nginx-health")[0] == 200:
                        break
                except (OSError, http.client.HTTPException):
                    pass
                time.sleep(0.25)
            else:
                raise RuntimeError(command("docker", "logs", edge))
            command("docker", "exec", edge, "nginx", "-t")
            for _ in range(120):
                ready = request("/api/readiness")
                if ready[0] == 200:
                    break
                time.sleep(0.25)
            else:
                raise AssertionError(("origin not ready", ready, command("docker", "logs", edge)))
            first_sitemap = request("/sitemap.xml")
            assert first_sitemap[0] == 200, first_sitemap
            assert first_sitemap[1]["X-Origin-Count"] == "1"
            assert request("/sitemap.xml")[1]["X-Origin-Count"] == "2", "default cache unexpectedly enabled"
            connections = set()
            for path in ["/api/reuse", "/v1/reuse"] * 4:
                status, headers, _ = request(path)
                assert status == 200 and headers["X-Origin-Host"] == "origin:8080", headers
                connections.add(headers["X-Origin-Connection"])
            assert len(connections) == 1, ("named pool not reused", connections)
            for path in ["/api/upgrade", "/v1/upgrade", "/ws"]:
                status, headers, _ = request(path, {"Upgrade": "websocket", "Connection": "Upgrade", "Accept-Encoding": "gzip, br"})
                assert status == 101 and headers["X-Origin-Encoding"] == "absent", (path, status, headers)
            assert request("/api/example.json")[0] == 200
            assert request("/v1/example.json")[0] == 200
            assert request("/asset.js")[2] == b"fixture-static"
            assert request("/client-route")[2] == b"fixture-spa"
            public_http = public_http.replace("# /sitemap.xml", "/sitemap.xml").replace("# /robots.txt", "/robots.txt")
            public_http = public_http.replace("    default off;", "    default off;\n    ~^/public/ foundation_public;")
            (conf / "public-cache.conf").write_text(public_http)
            command("docker", "exec", edge, "nginx", "-t")

            def workers():
                processes = command("docker", "exec", edge, "ps", "-o", "pid,args")
                return {line.split()[0] for line in processes.splitlines() if "nginx: worker process" in line}

            previous_workers = workers()
            assert previous_workers, "Nginx has no active worker before reload"
            command("docker", "exec", edge, "nginx", "-s", "reload")
            for _ in range(100):
                current_workers = workers()
                if current_workers and current_workers.isdisjoint(previous_workers):
                    break
                time.sleep(0.05)
            else:
                raise AssertionError(("old Nginx workers remain after reload", previous_workers, current_workers))
            cold = request("/sitemap.xml")
            assert cold[1].get("X-Fixture-Cache") == "MISS", cold
            warm = cold[1]["X-Origin-Count"]
            for method in ["GET", "HEAD"]:
                cached = request("/sitemap.xml", method=method)
                assert cached[1].get("X-Fixture-Cache") == "HIT" and cached[1]["X-Origin-Count"] == warm, (method, cached)
            for headers, method in [({"Authorization": "Bearer fixture"}, "GET"), ({"Cookie": "session=fixture"}, "GET"), ({"Authorization": "0"}, "GET"), ({"Cookie": "0"}, "GET"), ({"Range": "bytes=0-1"}, "GET"), ({}, "POST")]:
                assert request("/sitemap.xml", headers, method)[1]["X-Origin-Count"] != warm
            assert request("/sitemap.xml")[1]["X-Origin-Count"] == warm, "bypass polluted cache"
            for path in ["/sitemap.xml?q=1", "/sitemap.xml?0", "/public/private.xml", "/public/cookie.xml", "/public/failure.xml", "/api/uncached", "/v1/uncached", "/ws/plain"]:
                first = request(path)
                second = request(path)
                assert first[1]["X-Origin-Count"] != second[1]["X-Origin-Count"], ("unsafe cache", path)
            for variant in ["one", "two", "one"]:
                status, headers, _ = request("/public/vary.xml", {"X-Fixture-Variant": variant})
                assert status == 200 and headers["X-Origin-Variant"] == variant, headers
            with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                results = list(pool.map(request, ["/public/slow.xml"] * 8))
            assert {r[1]["X-Origin-Count"] for r in results} == {"1"}, "cold cache amplified origin work"
            request("/public/expired.xml")
            time.sleep(2.1)
            expired = request("/public/expired.xml")
            assert expired[1]["X-Origin-Count"] == "2", "upstream expiry was ignored"
            agent = 'crawler "quoted" \\ path'
            request("/robots.txt", {"User-Agent": agent})
            records = []
            for line in command("docker", "logs", edge).splitlines():
                if line.startswith('{"time":'):
                    records.append(json.loads(line))
            assert any(row.get("http_user_agent") == agent for row in records), "JSON escaping lost crawler identity"
            outcomes = {row.get("cache_status") for row in records}
            assert {"MISS", "HIT", "BYPASS", "EXPIRED"} <= outcomes, outcomes
            assert all(row.get("request_id") for row in records)
            print("PASS: nginx -t; default-off cache; named-pool reuse; Host and upgrades; API precedence; SPA fallback; public GET/HEAD cache; safety bypasses; Vary; expiry; cold coalescing; escaped JSON cache outcomes")
    finally:
        for container in [edge, origin]:
            subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        subprocess.run(["docker", "network", "rm", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)


if __name__ == "__main__":
    main()
