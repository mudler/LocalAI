#!/usr/bin/env bash
#
# Check that every published documentation URL still resolves after a rebuild.
#
# docs/redirects.txt lists one URL path per line, relative to the docs root
# (for example /features/chats/). Each one must either be a real page in the
# built site, or an HTML redirect stub (a Hugo alias) that leads to a real page
# within a few hops. Add a line to the list when a page is published. Never
# remove one: an old link must keep working.
#
# Usage: check-docs-redirects.sh <docs-public-dir> [redirects-file] [merged-root]
#   docs-public-dir  output of the docs Hugo build (docs/public)
#   redirects-file   default: docs/redirects.txt next to this repository's docs
#   merged-root      optional. The merged site (main site with docs/ inside
#                    it, after gen-redirects.sh ran). When given, each URL is
#                    also checked at the site root, where the docs used to live.

set -euo pipefail

PUBLIC=${1:?usage: check-docs-redirects.sh <docs-public-dir> [redirects-file] [merged-root]}
LIST=${2:-"$(dirname "$0")/../../docs/redirects.txt"}
MERGED=${3:-}
MAX_HOPS=4

[ -d "$PUBLIC" ] || { echo "check-docs-redirects: no such directory: $PUBLIC" >&2; exit 2; }
[ -f "$LIST" ] || { echo "check-docs-redirects: no such file: $LIST" >&2; exit 2; }

# Print the file that serves URL path $2 under root $1, or nothing.
page_file() {
  local root=$1 url=${2%/}
  if [ -f "${root}${url}/index.html" ]; then
    echo "${root}${url}/index.html"
  elif [ -f "${root}${url}.html" ]; then
    echo "${root}${url}.html"
  elif [ -f "${root}${url}" ] && [ -n "$url" ]; then
    echo "${root}${url}"
  elif [ -z "$url" ] && [ -f "${root}/index.html" ]; then
    echo "${root}/index.html"
  fi
}

# Print the redirect target path (starting with /) of a stub file, or nothing.
stub_target() {
  local target
  target=$(grep -oiE 'http-equiv="?refresh"?[^>]*url=[^">]+' "$1" | head -n 1 | sed -E 's/.*url=//' || true)
  [ -n "$target" ] || return 0
  # Keep the path only. Drop scheme, host and the /docs prefix.
  target=${target#http://}
  target=${target#https://}
  case "$target" in
    /*) ;;
    *) target=/${target#*/} ;;
  esac
  target=${target%%\#*}
  echo "${target#/docs}"
}

fail=0
checked=0
while IFS= read -r url; do
  case "$url" in ''|'#'*) continue ;; esac
  checked=$((checked + 1))
  current=$url
  hops=0
  while :; do
    file=$(page_file "$PUBLIC" "$current")
    if [ -z "$file" ]; then
      echo "FAIL ${url}: nothing is served at ${current}" >&2
      fail=$((fail + 1))
      break
    fi
    next=$(stub_target "$file")
    [ -z "$next" ] && break
    hops=$((hops + 1))
    if [ "$hops" -gt "$MAX_HOPS" ]; then
      echo "FAIL ${url}: more than ${MAX_HOPS} redirects" >&2
      fail=$((fail + 1))
      break
    fi
    current=$next
  done

  if [ -n "$MERGED" ] && [ -z "$(page_file "$MERGED" "$url")" ]; then
    echo "FAIL ${url}: not served at the site root" >&2
    fail=$((fail + 1))
  fi
done < "$LIST"

echo "check-docs-redirects: ${checked} URL(s) checked, ${fail} failure(s)"
[ "$fail" -eq 0 ]
