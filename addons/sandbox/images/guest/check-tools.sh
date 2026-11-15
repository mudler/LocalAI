#!/bin/sh
# Build-time check of the command-line tools of one profile of the guest image.
# The guest user cannot install packages, so a missing tool fails the image
# build. The tools and their check commands are the ones the add-on manifest
# lists for the profile (capabilities.toolDefinitions[bash].execution.profiles),
# the one list that the note for the model is built from.
#
#   sh check-tools.sh [profile] [manifest]
#
# The profile is base by default. The script fails when the profile is not in
# the manifest, when it has no checks, or when one check command fails.
set -eu
profile=${1:-base}
manifest=${2:-/usr/local/lib/localai/manifest.json}
command -v jq >/dev/null 2>&1 || { echo "check-tools: jq is needed to read the manifest" >&2; exit 1; }
[ -f "$manifest" ] || { echo "check-tools: no manifest at $manifest" >&2; exit 1; }
query='.capabilities.toolDefinitions[] | select(.name=="bash") | .execution.profiles[]? | select(.id==$p)'
if ! jq -e --arg p "$profile" "$query" "$manifest" >/dev/null 2>&1; then
  echo "check-tools: the manifest has no profile $profile" >&2
  exit 1
fi
# One line per tool: its name, a tab and one check command per line below it.
list=$(jq -r --arg p "$profile" "$query | .tools[] | .name as \$n | (.check // [])[] | \"\\(\$n)\\t\\(.)\"" "$manifest")
[ -n "$list" ] || { echo "check-tools: profile $profile has no checks" >&2; exit 1; }
# Every tool of the profile must have a check: a tool without one is not proven.
unchecked=$(jq -r --arg p "$profile" "$query | .tools[] | select((.check // []) | length == 0) | .name" "$manifest")
if [ -n "$unchecked" ]; then
  echo "check-tools: profile $profile: no check for: $unchecked" >&2
  exit 1
fi
fail=0
oldifs=$IFS
IFS='
'
for row in $list; do
  IFS=$oldifs
  tool=${row%%	*}
  cmd=${row#*	}
  # shellcheck disable=SC2086
  if ! $cmd >/dev/null 2>&1; then
    echo "guest tool not usable: $tool ($cmd), profile $profile" >&2
    fail=1
  fi
  IFS='
'
done
IFS=$oldifs
exit "$fail"
