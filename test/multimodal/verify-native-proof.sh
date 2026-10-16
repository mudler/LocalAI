#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Native candidate proof. Exit 0 only for a validated PASS outcome, 3 for a
# validated recorded FAIL or BLOCKED outcome, 1 for an invalid report or tests.
# With all MM_* artifact variables set the probe is re-run against the pinned
# native build; otherwise the committed report is re-validated (replay).
set -uo pipefail
scratch="$PWD/.sdd-work/task7"
mkdir -p "$scratch"
revision=$(git rev-parse HEAD)
committed=test/multimodal/native-proof-report.json
finish() {
    code=$?
    trap - EXIT
    printf 'revision=%s\nexit_status=%s\nmode=%s\nreport=%s\n' "$revision" "$code" "${mode:-unset}" "${report:-unset}" > "$scratch/native-proof-verify.log"
    exit "$code"
}
trap finish EXIT
python3 -I -m unittest discover -s test/multimodal -p test_native_reports.py -v 2>&1 | tee "$scratch/native-unittest.log"
tests=$(sed -n 's/^Ran \([0-9]*\) tests\{0,1\} in.*/\1/p' "$scratch/native-unittest.log")
if ! tail -1 "$scratch/native-unittest.log" | grep -q '^OK$' || [[ -z $tests || $tests -lt 30 ]]; then
    echo 'FAIL: checker tests missing or failed' >&2
    exit 1
fi
echo "checker tests: $tests passed"
python3 -I test/multimodal/check_reports.py inventory || exit 1
if [[ -n ${MM_MODEL:-} && -n ${MM_PROJECTOR:-} && -n ${MM_UPSTREAM_SOURCE:-} && -n ${MM_BUILD_DIR:-} && -n ${MM_DECODER_DIR:-} && -n ${MM_APPROVAL_RECORD:-} ]]; then
    mode=rerun
    report="$scratch/native-report.json"
    rm -f "$report"
    MM_PROBE_OUT="$report" python3 test/multimodal/native_probe/probe.py
    echo "probe exit status: $?"
else
    mode=replay
    report="$committed"
    echo "MM_* artifacts not set: validating the committed report without re-running the probe"
fi
[[ -f $report ]] || { echo "FAIL: no report at $report" >&2; exit 1; }
python3 -I test/multimodal/check_native_reports.py "$report"
exit $?
