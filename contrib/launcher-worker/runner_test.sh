#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
: "${LAUNCHER_VERIFY_WORKDIR:?Set LAUNCHER_VERIFY_WORKDIR for test scratch}"
mkdir -p "$LAUNCHER_VERIFY_WORKDIR"
tmp=$(mktemp -d "$LAUNCHER_VERIFY_WORKDIR/contract.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
expect_status() {
  local expected=$1; shift
  local actual=0
  "$@" > "$tmp/output" 2>&1 || actual=$?
  if [[ $actual != "$expected" ]]; then
    cat "$tmp/output"
    echo "expected $expected, got $actual: $*" >&2
    exit 1
  fi
}
for mode in '' config launch status ownership lifecycle ui integration release unknown; do
  expect_status 2 bash "$here/verify.sh" "$mode"
done
expect_status 2 bash "$here/verify.sh" baseline extra
expect_status 23 bash -c 'source "$1/lib.sh"; run_logged "$2/failure" bash -c "echo failed; exit 23"' _ "$here" "$tmp"
[[ $(cat "$tmp/failure.exit") == 23 ]]
grep -q failed "$tmp/failure.log"
expect_status 0 bash -c 'source "$1/lib.sh"; run_logged "$2/pass" true' _ "$here" "$tmp"
# A failed evidence sink must turn a successful command into failure.
expect_status 1 bash -c 'source "$1/lib.sh"; run_logged "$2/missing/output" true' _ "$here" "$tmp"
expect_status 1 python3 "$here/check-report.py" "$tmp/missing.json"
for data in 'not json' '{}' 'null' '[null]' '[]' \
  '[{"SuiteSucceeded":true,"SpecReports":[]}]' \
  '[{"SuiteSucceeded":false,"SpecReports":[{"State":"passed","LeafNodeType":"It"}]}]' \
  '[{"SuiteSucceeded":true,"SpecReports":[{"State":"passed","LeafNodeType":"BeforeSuite"}]}]' \
  '[{"SuiteSucceeded":true,"SpecReports":[{"State":"passed","LeafNodeType":"It"},{"State":"skipped","LeafNodeType":"It"}]}]' \
  '[{"SuiteSucceeded":true,"SpecReports":[{"State":"pending"}]}]' \
  '[{"SuiteSucceeded":true,"SpecReports":[{"State":"failed"}]}]'; do
  printf '%s\n' "$data" > "$tmp/report.json"
  expect_status 1 python3 "$here/check-report.py" "$tmp/report.json"
  expect_status 1 python3 -O "$here/check-report.py" "$tmp/report.json"
done
printf '%s\n' '[{"SuiteSucceeded":true,"SpecReports":[{"State":"passed","LeafNodeType":"It"}]}]' > "$tmp/report.json"
expect_status 0 python3 "$here/check-report.py" "$tmp/report.json"
source "$here/lib.sh"
# Mock df only for the boundary contract; real baseline uses the actual filesystem.
for bytes in 0 10737418239 invalid; do
  expect_status 1 bash -c 'source "$1/lib.sh"; bytes=$2; df() { printf "header\nfs 999 0 %s\n" "$bytes"; }; require_space .' _ "$here" "$bytes"
done
expect_status 0 bash -c 'source "$1/lib.sh"; df() { printf "header\nfs 999 0 10737418240\n"; }; require_space .' _ "$here"
(umask 077; prepare_tmp "$tmp/private/tmp")
[[ $(stat -c %a "$tmp/private") == 700 ]]
[[ $(stat -c %a "$tmp/private/tmp") == 1777 ]]
# Direct /tmp bind uses container ancestors, not restrictive host ancestors.
docker run --rm --platform linux/amd64 --user _apt \
  -v "$tmp/private/tmp:/tmp" ubuntu:22.04 sh -ec \
  'test -x /; test -w /tmp; f=$(mktemp /tmp/apt.conf.XXXXXX); rm "$f"'
echo 'Runner contract: PASS'
