#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/scripts/addons" "$tmp/core/http/react-ui/node_modules/.bin" "$tmp/bin"
cp "$root/scripts/addons/prepare.sh" "$tmp/scripts/addons/"
cat > "$tmp/bin/go" <<'TOOL'
#!/bin/sh
case "$1" in version) echo 'go version go1.26.1 linux/amd64';; env) echo 1;; install|mod) exit 0;; *) exit 1;; esac
TOOL
printf '#!/bin/sh\necho v22.14.0\n' > "$tmp/bin/node"
printf '#!/bin/sh\necho "libprotoc 26.1"\n' > "$tmp/bin/protoc"
printf '#!/bin/sh\necho "fake 1 1 999999999"\n' > "$tmp/bin/df"
printf '#!/bin/sh\nexit 0\n' > "$tmp/bin/npm"
printf '#!/bin/sh\nexit 99\n' > "$tmp/core/http/react-ui/node_modules/.bin/playwright"
chmod +x "$tmp/bin/"* "$tmp/core/http/react-ui/node_modules/.bin/playwright"
unset PLAYWRIGHT_CHROMIUM_PATH
PATH="$tmp/bin:$PATH" sh "$tmp/scripts/addons/prepare.sh" --prepare-build
# Full consumer preparation must reach the browser and propagate its failure.
if PATH="$tmp/bin:$PATH" sh "$tmp/scripts/addons/prepare.sh" --prepare; then
    echo 'consumer preparation unexpectedly omitted the browser' >&2
    exit 1
fi
