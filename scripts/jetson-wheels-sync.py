#!/usr/bin/env python3
"""Mirror the jetson-only wheel subset from pypi.jetson-ai-lab.io.

Downloads the wheels for the packages listed in .github/jetson-wheels.json
(one list per JetPack index path) into a local directory laid out exactly
like the upstream index (<dest>/<index>/<project>/<wheel>). The jetson-wheels
CI workflow publishes that directory as a scratch OCI image on ghcr, and
backend builds serve it as a local package index during pip install — see
pypi_mirror_server.py and installRequirements in
backend/python/common/libbackend.sh for the consuming side and the
motivation (recurring multi-hour upstream outages).

The sync is additive-with-pruning against a *successfully fetched* project
page: files no longer listed upstream are removed, but a package whose page
cannot be fetched is left exactly as mirrored last time. When the whole
upstream is unreachable the existing mirror is kept as-is (exit 0) so a CI
run during an outage never destroys the last known-good wheels; it only
fails (exit 2) when upstream is down AND there is nothing mirrored yet,
i.e. the bootstrap run has nothing to publish.

Standard library only. Usage:
    python3 scripts/jetson-wheels-sync.py --config .github/jetson-wheels.json \
        --index jp6/cu129 --dest wheels [--changed-file /tmp/changed]
"""

import argparse
import hashlib
import html.parser
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

DIST_SUFFIXES = (".whl", ".tar.gz", ".zip")
TIMEOUT = 120


def normalize(name):
    """PEP 503 project-name normalization."""
    return re.sub(r"[-_.]+", "-", name).lower()


class _LinkParser(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.hrefs = []

    def handle_starttag(self, tag, attrs):
        if tag == "a":
            for key, value in attrs:
                if key == "href" and value:
                    self.hrefs.append(value)


def parse_links(page, base_url):
    """Extract (filename, absolute_url, sha256|None) for each dist link."""
    parser = _LinkParser()
    parser.feed(page)
    links = []
    for href in parser.hrefs:
        split = urllib.parse.urlsplit(href)
        filename = os.path.basename(urllib.parse.unquote(split.path))
        if not filename.endswith(DIST_SUFFIXES):
            continue
        sha256 = None
        if split.fragment.startswith("sha256="):
            sha256 = split.fragment[len("sha256="):]
        url = urllib.parse.urljoin(base_url, split._replace(fragment="").geturl())
        links.append((filename, url, sha256))
    return links


def _fetch(url):
    request = urllib.request.Request(url, headers={"User-Agent": "localai-jetson-wheels-sync"})
    return urllib.request.urlopen(request, timeout=TIMEOUT)


def _download(url, dest_path, sha256):
    digest = hashlib.sha256()
    tmp = dest_path + ".tmp"
    with _fetch(url) as resp, open(tmp, "wb") as out:
        for chunk in iter(lambda: resp.read(1 << 20), b""):
            digest.update(chunk)
            out.write(chunk)
    if sha256 and digest.hexdigest() != sha256:
        os.unlink(tmp)
        raise RuntimeError(f"sha256 mismatch for {url}: expected {sha256}, got {digest.hexdigest()}")
    os.replace(tmp, dest_path)


def _has_wheels(dest):
    for _, _, files in os.walk(dest):
        if any(f.endswith(DIST_SUFFIXES) for f in files):
            return True
    return False


def sync_package(base_url, package, dest_dir):
    """Returns (fetched_ok, changed)."""
    page_url = urllib.parse.urljoin(base_url, normalize(package) + "/")
    try:
        with _fetch(page_url) as resp:
            page = resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        if err.code == 404:
            # Upstream simply doesn't host this package for this index —
            # normal (the config list is a superset across JetPack versions).
            print(f"  {package}: not hosted upstream (404), skipping")
            return True, False
        print(f"  {package}: upstream error {err.code}, keeping mirrored files")
        return False, False
    except (urllib.error.URLError, TimeoutError, OSError) as err:
        print(f"  {package}: upstream unreachable ({err}), keeping mirrored files")
        return False, False

    links = parse_links(page, page_url)
    listed = {name for name, _, _ in links}
    changed = False
    os.makedirs(dest_dir, exist_ok=True)
    for name, url, sha256 in links:
        path = os.path.join(dest_dir, name)
        if os.path.exists(path):
            continue
        print(f"  {package}: downloading {name}")
        _download(url, path, sha256)
        changed = True
    # Prune only against a page we actually fetched: upstream removing a
    # wheel is tracked, an outage never empties the mirror.
    for existing in os.listdir(dest_dir):
        if existing.endswith(DIST_SUFFIXES) and existing not in listed:
            print(f"  {package}: pruning {existing} (no longer listed upstream)")
            os.unlink(os.path.join(dest_dir, existing))
            changed = True
    return True, changed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--index", required=True, help="index path, e.g. jp6/cu129")
    parser.add_argument("--dest", required=True)
    parser.add_argument("--upstream", help="override the config's upstream (for tests)")
    parser.add_argument("--changed-file", help="created iff the mirror content changed")
    args = parser.parse_args()

    with open(args.config) as f:
        config = json.load(f)
    packages = config["indexes"][args.index]
    upstream = args.upstream or config["upstream"]
    base_url = upstream.rstrip("/") + "/" + args.index.strip("/") + "/"

    print(f"syncing {args.index} from {base_url}: {', '.join(packages)}")
    any_fetched = False
    any_changed = False
    for package in packages:
        dest_dir = os.path.join(args.dest, args.index, normalize(package))
        fetched, changed = sync_package(base_url, package, dest_dir)
        any_fetched = any_fetched or fetched
        any_changed = any_changed or changed

    if not any_fetched:
        if _has_wheels(os.path.join(args.dest, args.index)):
            print("upstream unreachable; keeping existing mirror unchanged")
            return 0
        print("upstream unreachable and nothing mirrored yet — nothing to publish")
        return 2
    if any_changed and args.changed_file:
        with open(args.changed_file, "w") as f:
            f.write("changed\n")
    print("sync complete" + (" (changes)" if any_changed else " (no changes)"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
