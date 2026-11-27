#!/usr/bin/env bash
# Checks of the curated plugin marketplace (plugins/.claude-plugin/marketplace.json)
# that the JSON schema and `local-ai plugins validate` do not cover:
#
#   1. pins:    every entry is a github or git-subdir source with a full SHA.
#   2. license: the license file at the pinned SHA (in the plugin folder, else
#               at the repository root) has the SPDX ID of metadata.localai.license.
#   3. self:    an entry of this repository is pinned to a commit whose plugin
#               folder equals the folder in the checkout. A pull request that
#               changes the folder gets a warning (the bot pins it after the
#               merge); any other pull request fails.
#
# It writes a copy of the marketplace to OUT without the self entries that
# wait for the bot, so that `local-ai plugins validate --marketplace OUT`
# fetches only commits that hold the plugin. The folder of such an entry is
# validated from the checkout instead.
#
# Usage: plugins-marketplace-check.sh MARKETPLACE OUT
# Environment:
#   BASE_SHA  the base commit of the pull request (optional; without it a
#             mismatch of a self entry is an error)
#   SELF_URL  the git URL of this repository
#             (default https://github.com/${GITHUB_REPOSITORY:-mudler/LocalAI}.git)
#   SCRATCH   a folder for the license fetches (default: a new folder under TMPDIR)
set -euo pipefail

marketplace=${1:?usage: $0 MARKETPLACE OUT}
out=${2:?usage: $0 MARKETPLACE OUT}
self_url=${SELF_URL:-https://github.com/${GITHUB_REPOSITORY:-mudler/LocalAI}.git}
base_sha=${BASE_SHA:-}
scratch=${SCRATCH:-$(mktemp -d)}
mkdir -p "$scratch"
trap 'rm -rf "$scratch"' EXIT

failed=0
fail() { echo "::error file=$marketplace::$*"; failed=1; }
warn() { echo "::warning file=$marketplace::$*"; }

# git for the fetches of third-party repositories: https only, no prompt, no
# hooks. Nothing is checked out, so no filter or attribute runs either.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
sgit() {
  git -c protocol.allow=never -c protocol.https.allow=always \
      -c core.hooksPath=/dev/null -c core.fsmonitor=false "$@"
}

# spdx_of prints the SPDX ID of a license text on stdin, or "unknown".
spdx_of() {
  local text id
  text=$(tr -s '[:space:]' ' ')
  id=$(grep -oE 'SPDX-License-Identifier: *[A-Za-z0-9.+-]+' <<<"$text" | head -n1 | sed 's/.*: *//') || true
  if [ -n "$id" ]; then echo "$id"; return; fi
  case "$text" in
    *"Apache License"*"Version 2.0"*) echo "Apache-2.0" ;;
    *"Mozilla Public License Version 2.0"*) echo "MPL-2.0" ;;
    *"free and unencumbered software released into the public domain"*) echo "Unlicense" ;;
    *"CC0 1.0 Universal"*) echo "CC0-1.0" ;;
    *"Permission is hereby granted, free of charge"*) echo "MIT" ;;
    *"Redistribution and use in source and binary forms"*"Neither the name"*) echo "BSD-3-Clause" ;;
    *"Redistribution and use in source and binary forms"*) echo "BSD-2-Clause" ;;
    *"Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that"*) echo "ISC" ;;
    *"Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted."*) echo "0BSD" ;;
    *) echo "unknown" ;;
  esac
}

# license_at prints "<path> <SPDX ID>" for the license file of a plugin at a
# commit: the first one in the plugin folder, else the one at the root.
license_at() {
  local url=$1 sha=$2 dir=$3 repo f
  repo=$(mktemp -d "$scratch/repo.XXXXXX")
  sgit -C "$repo" init -q
  sgit -C "$repo" remote add origin "$url"
  # A partial fetch of the one commit: trees now, each blob when it is read.
  sgit -C "$repo" fetch -q --depth 1 --filter=blob:none origin "$sha"
  for prefix in ${dir:+"$dir/"} "."; do
    f=$(sgit -C "$repo" ls-tree --name-only "$sha" -- "$prefix" \
          | grep -iE '(^|/)(LICENSE|LICENCE|COPYING)(\.(md|txt))?$' | head -n1) || true
    if [ -n "$f" ]; then
      echo "$f $(sgit -C "$repo" cat-file blob "$sha:$f" | spdx_of)"
      return
    fi
  done
  echo "- none"
}

normalize_url() { sed -E 's#/*$##; s#\.git$##' <<<"$1" | tr '[:upper:]' '[:lower:]'; }

count=$(jq '.plugins | length' "$marketplace")
skip_self=()
for i in $(seq 0 $((count - 1))); do
  entry=$(jq -c ".plugins[$i]" "$marketplace")
  name=$(jq -r '.name' <<<"$entry")
  kind=$(jq -r '.source.source? // "relative"' <<<"$entry")
  sha=$(jq -r '.source.sha? // ""' <<<"$entry")
  want=$(jq -r '.metadata.localai.license? // ""' <<<"$entry")

  # 1. pins
  case "$kind" in
    github)
      url="https://github.com/$(jq -r '.source.repo' <<<"$entry").git"
      dir="" ;;
    git-subdir)
      url=$(jq -r '.source.url' <<<"$entry")
      dir=$(jq -r '.source.path' <<<"$entry") ;;
    *)
      fail "$name: source $kind is refused; use github or git-subdir with a full sha"
      continue ;;
  esac
  if ! [[ "$sha" =~ ^[0-9a-f]{40}$ ]]; then
    fail "$name: source.sha must be a full 40-hex commit SHA, got '$sha'"
    continue
  fi
  if [[ "$url" != https://* ]]; then
    fail "$name: the source URL must use https, got '$url'"
    continue
  fi

  # 2. license
  if ! read -r lic_file got < <(license_at "$url" "$sha" "$dir"); then
    fail "$name: cannot fetch $url at $sha"
    continue
  fi
  if [ "$lic_file" = "-" ]; then
    fail "$name: no license file in $dir or at the root of $url at $sha"
  elif [ "$got" != "$want" ]; then
    fail "$name: metadata.localai.license is '$want', but $lic_file at $sha reads as '$got'"
  else
    echo "ok    $name: $lic_file is $got"
  fi

  # 3. the entries of this repository
  if [ "$kind" = git-subdir ] && [ "$(normalize_url "$url")" = "$(normalize_url "$self_url")" ]; then
    git fetch -q --depth 1 origin "$sha" 2>/dev/null || true
    if git cat-file -e "$sha^{commit}" 2>/dev/null && git diff --quiet "$sha" HEAD -- "$dir"; then
      echo "ok    $name: $dir at $sha equals the checkout"
    elif [ -n "$base_sha" ] && ! git diff --quiet "$base_sha" HEAD -- "$dir"; then
      warn "$name: this pull request changes $dir, so the pinned sha $sha is behind. After the merge, a bot pull request pins it to the merge commit. The folder is validated from the checkout."
      skip_self+=("$name")
    else
      fail "$name: $dir at the pinned sha $sha differs from the folder in this branch. Merge the pending pin pull request, or set the sha to the commit that last changed $dir."
    fi
  fi
done

skip_json=$(printf '%s\n' "${skip_self[@]+"${skip_self[@]}"}" | jq -R . | jq -sc 'map(select(. != ""))')
jq --argjson skip "$skip_json" '.plugins |= map(select(.name as $n | $skip | index($n) | not))' \
  "$marketplace" >"$out"

exit "$failed"
