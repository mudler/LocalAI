#!/bin/sh
# Build-time check of the command-line tools in the guest image. The guest user
# cannot install packages, so a missing tool fails the image build. The list
# must equal GuestTools in core/services/studio/sandbox/guesttools.go.
set -eu
fail=0
for cmd in "pdftoppm -v" "pdftotext -v" "jq --version" "zip -v" "unzip -v" \
    "file --version" "sqlite3 --version" "rg --version" "tree --version" \
    "git --version" "curl --version"; do
  # shellcheck disable=SC2086
  if ! $cmd >/dev/null 2>&1; then
    echo "guest tool not usable: $cmd" >&2
    fail=1
  fi
done
exit "$fail"
