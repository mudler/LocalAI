#!/usr/bin/env python3
"""Check the internal links of the built documentation site.

Usage: check-docs-links.py <docs-public-dir> [--base /docs/] [--fragments]

Reads every HTML page under the build output (the print pages are skipped). For each href and src that
points inside the site, it checks that the target file exists. With
--fragments it also checks that the #anchor exists in the target page.
Links to other hosts are not checked here. Exit status 1 when a link is
broken.
"""
import argparse
import os
import re
import sys
import urllib.parse


ATTR = r"""(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))"""
ID_RE = re.compile(r"\s(?:id|name)=" + ATTR, re.I)
LINK_RE = re.compile(r"<(?:a|img|link|script|source|video|audio|iframe)\b[^>]*?\s(?:href|src)=" + ATTR, re.I | re.S)


class Collector:
    """Collect the ids and the links of one page.

    A regular expression is used on purpose. Pages can hold raw HTML in their
    text, and a strict HTML parser then loses everything after it.
    """

    def __init__(self):
        self.links = []
        self.ids = set()

    def feed(self, text):
        for m in ID_RE.finditer(text):
            self.ids.add(next(g for g in m.groups() if g is not None))
        for m in LINK_RE.finditer(text):
            self.links.append(next(g for g in m.groups() if g is not None))


def served_file(root, path):
    """Map a URL path inside the site to a file, or None."""
    p = root + path
    if os.path.isfile(p):
        return p
    if os.path.isfile(p.rstrip("/") + "/index.html"):
        return p.rstrip("/") + "/index.html"
    if os.path.isfile(p.rstrip("/") + ".html"):
        return p.rstrip("/") + ".html"
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("--base", default="/docs/", help="URL prefix of the site")
    ap.add_argument("--fragments", action="store_true")
    args = ap.parse_args()
    root = args.root.rstrip("/")
    base = args.base if args.base.endswith("/") else args.base + "/"

    pages = {}
    for dirpath, _, files in os.walk(root):
        for name in files:
            if not name.endswith(".html") or "searchindex" in name or name.endswith(".print.html"):
                continue
            path = os.path.join(dirpath, name)
            c = Collector()
            with open(path, encoding="utf-8", errors="replace") as f:
                c.feed(f.read())
            pages[path] = c

    bad = []
    checked = 0
    for path, c in sorted(pages.items()):
        rel = "/" + os.path.relpath(os.path.dirname(path), root).replace(os.sep, "/") + "/"
        rel = base[:-1] + rel.replace("/./", "/")
        for link in c.links:
            if re.match(r"^(https?:|mailto:|javascript:|data:|tel:|//)", link):
                if not link.startswith("https://localai.io" + base):
                    continue
                link = link[len("https://localai.io"):]
            url, _, frag = link.partition("#")
            url = url.split("?")[0]
            if not url and not frag:
                continue
            target = urllib.parse.urljoin(rel, url) if url else rel
            if not target.startswith(base):
                continue  # outside this site (the main site owns it)
            checked += 1
            f = served_file(root, target[len(base) - 1:])
            if f is None:
                bad.append((path, link, "no such page or file"))
                continue
            if args.fragments and frag and f.endswith(".html"):
                if f not in pages:
                    continue
                if urllib.parse.unquote(frag) not in pages[f].ids:
                    bad.append((path, link, "no such anchor"))

    for path, link, why in bad:
        print("BROKEN %s: %s (%s)" % (os.path.relpath(path, root), link, why), file=sys.stderr)
    print("check-docs-links: %d link(s) checked, %d broken" % (checked, len(bad)))
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
