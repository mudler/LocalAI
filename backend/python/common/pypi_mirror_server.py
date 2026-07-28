"""Ephemeral PEP 503 "simple" index over a local directory of wheels.

Serves a directory tree laid out like a package index (e.g.
``<root>/jp6/cu129/torch/torch-2.8.0-cp312-...whl``) as a standards-compliant
"simple" index on localhost, so uv/pip can resolve against it exactly as they
would against the real remote index — same per-project pages, same 404
fall-through to PyPI for projects the mirror does not carry.

This exists because pypi.jetson-ai-lab.io (the only source of CUDA-enabled
aarch64 torch wheels for JetPack) has a history of multi-hour 502 outages,
and an --extra-index-url that errors is fatal to the whole resolution: uv
consults every configured index for every requirement, so one 502 on any
project page kills the install even for projects hosted on PyPI. CI mirrors
the handful of jetson-only wheels into an OCI image, bind-mounts it into the
backend build, and libbackend.sh serves it with this script while rewriting
the index host in the requirements files to 127.0.0.1 (see
installRequirements in libbackend.sh). A 404 from this server is a clean
"not here" that resolvers follow up on PyPI; the upstream 502 never was.

Standard library only — it runs inside every python backend's build
container, before any venv exists.

Usage:
    python3 pypi_mirror_server.py --root /jetson-wheels --port-file /tmp/port

Run the tests standalone:
    python3 -m unittest pypi_mirror_server_test
"""

import argparse
import hashlib
import html
import os
import re
import sys
import threading
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Extensions treated as distribution files: a directory containing at least
# one of these is a project page; any other directory is a sub-index listing.
DIST_SUFFIXES = (".whl", ".tar.gz", ".zip")

_hash_cache = {}
_hash_lock = threading.Lock()


def normalize(name):
    """PEP 503 project-name normalization."""
    return re.sub(r"[-_.]+", "-", name).lower()


def _file_sha256(path):
    """sha256 of a file, cached on (path, mtime, size) — wheels are large."""
    st = os.stat(path)
    key = (path, st.st_mtime_ns, st.st_size)
    with _hash_lock:
        cached = _hash_cache.get(key)
    if cached:
        return cached
    digest = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            digest.update(chunk)
    value = digest.hexdigest()
    with _hash_lock:
        _hash_cache[key] = value
    return value


def resolve_path(root, url_path):
    """Map a URL path onto the tree under root, or None.

    Path segments match either exactly or via PEP 503 normalization
    (resolvers request ``liquid-audio`` even if the directory on disk is
    named ``liquid_audio``). Rejects any segment that would escape root.
    """
    current = root
    for segment in url_path.split("/"):
        if segment in ("", "."):
            continue
        if segment == ".." or "/" in segment or "\\" in segment:
            return None
        candidate = os.path.join(current, segment)
        if not os.path.exists(candidate):
            try:
                entries = os.listdir(current)
            except (NotADirectoryError, FileNotFoundError):
                return None
            wanted = normalize(segment)
            matches = [e for e in entries if normalize(e) == wanted]
            if not matches:
                return None
            candidate = os.path.join(current, matches[0])
        current = candidate
    return current


class SimpleIndexHandler(BaseHTTPRequestHandler):
    root = None
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        self._respond(head_only=False)

    def do_HEAD(self):
        self._respond(head_only=True)

    def _respond(self, head_only):
        url_path = urllib.parse.unquote(urllib.parse.urlsplit(self.path).path)
        local = resolve_path(self.root, url_path)
        if local is None:
            self._send_error(404, "not found")
            return
        if os.path.isfile(local):
            self._send_file(local, head_only)
            return
        # Relative hrefs on index pages resolve against the request URL, so
        # directory URLs must end in "/" — redirect like real indexes do.
        if not url_path.endswith("/"):
            self.send_response(301)
            self.send_header("Location", self.path + "/")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        entries = sorted(os.listdir(local))
        files = [e for e in entries if e.endswith(DIST_SUFFIXES)]
        if files:
            body = self._project_page(local, files)
        else:
            dirs = [e for e in entries if os.path.isdir(os.path.join(local, e))]
            body = self._listing_page(dirs)
        self._send_html(body, head_only)

    def _project_page(self, project_dir, files):
        anchors = []
        for name in files:
            digest = _file_sha256(os.path.join(project_dir, name))
            anchors.append(
                '<a href="%s#sha256=%s">%s</a><br/>'
                % (urllib.parse.quote(name), digest, html.escape(name))
            )
        return self._page(anchors)

    def _listing_page(self, dirs):
        anchors = [
            '<a href="%s/">%s</a><br/>'
            % (urllib.parse.quote(normalize(d)), html.escape(normalize(d)))
            for d in dirs
        ]
        return self._page(anchors)

    def _page(self, anchors):
        return (
            "<!DOCTYPE html><html><head>"
            '<meta name="pypi:repository-version" content="1.0">'
            "<title>simple index</title></head><body>\n"
            + "\n".join(anchors)
            + "\n</body></html>"
        ).encode()

    def _send_html(self, body, head_only):
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if not head_only:
            self.wfile.write(body)

    def _send_file(self, path, head_only):
        size = os.path.getsize(path)
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", str(size))
        self.end_headers()
        if head_only:
            return
        with open(path, "rb") as f:
            while True:
                chunk = f.read(1 << 20)
                if not chunk:
                    break
                self.wfile.write(chunk)

    def _send_error(self, code, message):
        body = message.encode()
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        sys.stderr.write("pypi-mirror: %s\n" % (format % args))


def make_server(root, host="127.0.0.1", port=0):
    handler = type("Handler", (SimpleIndexHandler,), {"root": os.path.abspath(root)})
    return ThreadingHTTPServer((host, port), handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, help="directory tree to serve")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=0, help="0 picks a free port")
    parser.add_argument(
        "--port-file",
        help="write the bound port here once listening (readiness signal)",
    )
    args = parser.parse_args()

    server = make_server(args.root, args.host, args.port)
    port = server.server_address[1]
    if args.port_file:
        # Write-then-rename so a reader never sees a partially written port.
        tmp = args.port_file + ".tmp"
        with open(tmp, "w") as f:
            f.write(str(port))
        os.replace(tmp, args.port_file)
    sys.stderr.write("pypi-mirror: serving %s on %s:%d\n" % (args.root, args.host, port))
    server.serve_forever()


if __name__ == "__main__":
    main()
