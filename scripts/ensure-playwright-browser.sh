#!/usr/bin/env sh
#
# Ensure a Chromium is available for the Playwright e2e suite, with an
# actionable error instead of a cryptic apt failure.
#
# Resolution order:
#   1. PLAYWRIGHT_CHROMIUM_PATH set  -> use it (the nix flake dev shell exports
#      this; playwright.config.js reads it). Just validate it's executable.
#   2. apt-get available (CI/Debian) -> `playwright install --with-deps chromium`.
#   3. otherwise                     -> fail with guidance (e.g. NixOS without
#      the dev shell, where the downloaded browser can't resolve system libs).
#
# Run from core/http/react-ui (so `bunx playwright` resolves the local install).
set -eu

if [ -n "${PLAYWRIGHT_CHROMIUM_PATH:-}" ]; then
	if [ ! -x "$PLAYWRIGHT_CHROMIUM_PATH" ]; then
		echo "ensure-playwright-browser: PLAYWRIGHT_CHROMIUM_PATH is set but not executable:" >&2
		echo "  $PLAYWRIGHT_CHROMIUM_PATH" >&2
		exit 1
	fi
	echo "ensure-playwright-browser: using PLAYWRIGHT_CHROMIUM_PATH ($PLAYWRIGHT_CHROMIUM_PATH)"
	exit 0
fi

if command -v apt-get >/dev/null 2>&1; then
	echo "ensure-playwright-browser: installing Playwright Chromium (--with-deps)…"
	exec bunx playwright install --with-deps chromium
fi

cat >&2 <<'MSG'
ensure-playwright-browser: no Chromium available for Playwright.

PLAYWRIGHT_CHROMIUM_PATH is not set, and this system has no apt-get to install
Playwright's bundled browser with system libraries (e.g. NixOS — the bundled
browser can't resolve libglib-2.0 and friends).

Fix one of:
  • Enter the dev shell:   nix develop
      (provides chromium and exports PLAYWRIGHT_CHROMIUM_PATH)
  • Or point at a Chromium yourself:
      export PLAYWRIGHT_CHROMIUM_PATH=/path/to/chromium

then re-run. (CI uses the apt-get path automatically.)
MSG
exit 1
