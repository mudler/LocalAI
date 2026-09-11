#!/usr/bin/env bash
# Refreshes the counters shown on the landing page from the GitHub API.
#
# The numbers used to be typed into the templates by hand, which meant they
# only moved when somebody remembered, and a stale star count on the front
# page is worse than no star count. Everything the API can answer for lives
# in website/data/stats.yaml and is rewritten wholesale by this script.
#
# Anything the API cannot answer for (the Discord member count) is read back
# out of the existing file and carried through untouched.
set -euo pipefail

REPO="${REPO:-mudler/LocalAI}"
OUT="${OUT:-website/data/stats.yaml}"

# The contributors and releases endpoints are paginated and never report a
# total. Asking for one item per page makes the last page number equal to the
# item count, which the Link header hands over.
count_via_link_header() {
  local path="$1" link last
  link=$(gh api -i "${path}?per_page=1" 2>/dev/null | tr -d '\r' | grep -i '^link:' || true)
  if [ -z "$link" ]; then
    # No Link header means a single page, so count that page directly.
    gh api "${path}?per_page=100" --jq 'length'
    return
  fi
  last=$(sed -n 's/.*[?&]page=\([0-9]*\)>; rel="last".*/\1/p' <<<"$link")
  [ -n "$last" ] || { gh api "${path}?per_page=100" --jq 'length'; return; }
  printf '%s\n' "$last"
}

read -r stars forks < <(gh api "repos/${REPO}" --jq '"\(.stargazers_count) \(.forks_count)"')
contributors=$(count_via_link_header "repos/${REPO}/contributors")
releases=$(count_via_link_header "repos/${REPO}/releases")

# Not derivable from the GitHub API, so keep whatever is already on disk.
discord=$(sed -n 's/^discord: *\([0-9]*\).*/\1/p' "$OUT" 2>/dev/null | head -1)
discord="${discord:-0}"

for n in stars forks contributors releases; do
  v="${!n}"
  [[ "$v" =~ ^[0-9]+$ ]] && [ "$v" -gt 0 ] || {
    echo "refusing to write: ${n} came back as '${v}'" >&2
    exit 1
  }
done

cat > "$OUT" <<YAML
# Counters shown on the landing page.
#
# The four GitHub fields are rewritten by .github/ci/refresh-site-counters.sh,
# which runs weekly from .github/workflows/refresh-site-counters.yml. Editing
# them by hand works but will be overwritten on the next run.
stars: ${stars}
forks: ${forks}
contributors: ${contributors}
releases: ${releases}

# The GitHub API cannot answer for this one, so it is maintained by hand and
# the refresh script carries it through untouched.
discord: ${discord}
YAML

echo "stars=${stars} forks=${forks} contributors=${contributors} releases=${releases} discord=${discord}"
