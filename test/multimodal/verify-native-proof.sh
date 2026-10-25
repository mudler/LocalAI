#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Native candidate proof. Exit 0 only for a validated PASS outcome, 3 for a
# validated recorded FAIL or BLOCKED outcome, 1 for an invalid report or tests.
# Contract v1 is the upstream-server proof and stays as recorded. Contract v2 is
# the proof of the LocalAI llama-cpp backend and decides the exit status.
# With all MM_* artifact variables set (and MM_E2E_BACKEND for v2) the proof is
# re-run; otherwise the committed reports are recomputed from the committed raw
# vectors (replay).
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
v1=$?
# Contract v1 (the upstream llama-server, video included) stays a recorded FAIL or
# PASS. Exit 1 is an invalid report and stops here.
[[ $v1 == 0 || $v1 == 3 ]] || exit 1
echo "contract v1 (upstream server, with video): exit status $v1"

# Contract v2 is the current proof: the LocalAI llama-cpp backend, text, image and
# audio. The adapter checker tests must pass before its verdict is used.
python3 -I -m unittest discover -s test/multimodal -p test_adapter_proof.py -v 2>&1 | tee "$scratch/adapter-unittest.log"
adapter_tests=$(sed -n 's/^Ran \([0-9]*\) tests\{0,1\} in.*/\1/p' "$scratch/adapter-unittest.log")
if ! grep -q '^OK$' "$scratch/adapter-unittest.log" || [[ -z $adapter_tests || $adapter_tests -lt 8 ]]; then
    echo 'FAIL: adapter checker tests missing or failed' >&2
    exit 1
fi
echo "adapter checker tests: $adapter_tests passed"
contract=test/multimodal/acceptance-contract-v2.json
fixtures=test/multimodal/fixtures-v2
if [[ -n ${MM_E2E_BACKEND:-} && -n ${MM_MODEL:-} && -n ${MM_PROJECTOR:-} ]]; then
    export MM_E2E_REAL=1 MM_E2E_CONTRACT=$PWD/$contract MM_E2E_FIXTURES=$PWD/$fixtures
    export MM_E2E_EMBED_MODEL=$MM_MODEL MM_E2E_EMBED_PROJECTOR=$MM_PROJECTOR MM_E2E_VECTORS_OUT=$scratch/v2-vectors.json
    export MM_E2E_LOCALAI_REVISION=$revision
    go test ${MM_GO_TEST_FLAGS:-} ./tests/e2e -count=1 -timeout 60m -ginkgo.label-filter=MultimodalEmbeddingReal -ginkgo.focus 'frozen contract' || exit 1
    python3 -I test/multimodal/check_adapter_proof.py "$contract" "$scratch/v2-vectors.json" "$fixtures" --write "$scratch/v2-report.json"
    exit $?
fi
# Replay: recompute the committed report from the committed raw vectors.
python3 -I test/multimodal/check_adapter_proof.py "$contract" test/multimodal/native-proof-v2-vectors.json "$fixtures" --report test/multimodal/native-proof-v2-report.json
v2=$?
# The same fixtures as v1, scored through the adapter (no video), recorded for comparison.
python3 -I test/multimodal/check_adapter_proof.py test/multimodal/acceptance-contract.json test/multimodal/native-proof-v1-adapter-vectors.json test/multimodal/fixtures --report test/multimodal/native-proof-v1-adapter-report.json
[[ $? == 0 || $? == 3 ]] || exit 1
exit $v2
