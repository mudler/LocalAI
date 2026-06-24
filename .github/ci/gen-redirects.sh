#!/usr/bin/env bash
#
# Generate client-side redirects for the documentation URLs that used to live at
# the site root.
#
# Until this site existed, the Hugo docs site WAS localai.io, so pages
# were published at /features/..., /getting-started/..., /faq/ and so on. The
# docs now build under /docs/, and GitHub Pages serves static files only: there
# is no server-side rewrite, no .htaccess, no _redirects. The only way to keep
# every published, bookmarked and search-indexed URL alive is to leave a real
# HTML file at the old address that sends the browser to the new one.
#
# Anything the main site already publishes wins: it owns /, /engines/,
# /blog/ and friends, so an existing file is never replaced.
#
# Usage: gen-redirects.sh <public-dir> [base-url]
#   public-dir  merged output directory (main site with docs/ inside it)
#   base-url    absolute or root-relative prefix the deployment is served from,
#               trailing slash optional (default "/")

set -euo pipefail

PUBLIC_DIR=${1:?usage: gen-redirects.sh <public-dir> [base-url]}
BASE_URL=${2:-/}

# Normalise to exactly one trailing slash so concatenation below is predictable.
BASE_URL="${BASE_URL%/}/"

DOCS_DIR="${PUBLIC_DIR}/docs"

if [ ! -d "$DOCS_DIR" ]; then
  echo "gen-redirects: no docs output at ${DOCS_DIR}" >&2
  exit 1
fi

created=0
skipped=0

# Every .html file is a reachable old URL, not just directory indexes: the
# generated model gallery ships as a bare gallery.html and used to sit at the
# root too.
while IFS= read -r src; do
  rel=${src#"$DOCS_DIR"/}
  dst="${PUBLIC_DIR}/${rel}"

  if [ -e "$dst" ]; then
    skipped=$((skipped + 1))
    continue
  fi

  # Link to the directory, not to its index.html, so the redirect target is the
  # canonical URL the docs site itself advertises.
  target="${BASE_URL}docs/${rel%index.html}"

  mkdir -p "$(dirname "$dst")"
  printf '%s' '<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Moved</title>
<link rel="canonical" href="'"$target"'">
<meta name="robots" content="noindex">
<meta http-equiv="refresh" content="0; url='"$target"'">
</head>
<body>
<p>This page moved to <a href="'"$target"'">'"$target"'</a>.</p>
</body>
</html>
' > "$dst"

  created=$((created + 1))
done <<EOF
$(find "$DOCS_DIR" -type f -name '*.html' | sort)
EOF

echo "gen-redirects: ${created} redirect(s) written, ${skipped} path(s) left to the main site"
