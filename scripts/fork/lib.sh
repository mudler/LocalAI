#!/bin/sh
# Shared helpers for scripts/fork/*. Sourced, never executed directly.
# Reads .github/fork/branches.yml via python3 (PyYAML) so the manifest stays YAML.

set -eu

FORK_ROOT=$(git rev-parse --show-toplevel)
MANIFEST="$FORK_ROOT/.github/fork/branches.yml"

die() { echo "error: $*" >&2; exit 1; }

# manifest_get <python-expression-on-d>  e.g. "d['upstream']['remote']"
manifest_get() {
    python3 -c "
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
print($1)
" "$MANIFEST"
}

upstream_ref() {
    remote=$(manifest_get "d['upstream']['remote']")
    branch=$(manifest_get "d['upstream']['branch']")
    echo "$remote/$branch"
}

overlay_branch() {
    manifest_get "d['overlay']"
}

# Prints one line per PR branch: "<name>\t<upstream_pr or ->"
pr_branches() {
    python3 -c "
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
for b in d.get('branches', []):
    print(b['name'] + '\t' + str(b.get('upstream_pr') or '-'))
" "$MANIFEST"
}

# Resolve a manifest branch name to a ref: local branch if present, else origin/<name>.
resolve_ref() {
    if git show-ref --verify --quiet "refs/heads/$1"; then
        echo "$1"
    elif git show-ref --verify --quiet "refs/remotes/origin/$1"; then
        echo "origin/$1"
    else
        die "branch '$1' not found locally or on origin"
    fi
}
