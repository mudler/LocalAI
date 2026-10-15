#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Bounded acquisition and frontend admission. Pure Go: no network, no native
# backend and no downloads; every HTTP exchange targets an owned test server
# through injected resolver and dialer hooks, with the production address
# policy still enforced.
set -euo pipefail
scratch="$PWD/.sdd-work/task4/verify"
mkdir -p "$scratch"
revision=$(git rev-parse HEAD)
finish() {
 code=$?
 trap - EXIT
 printf 'revision=%s\nexit_status=%s\nevidence=%s\n' "$revision" "$code" "$scratch" > "$scratch/verify.log"
 exit "$code"
}
trap finish EXIT
rm -f "$scratch/acquisition.json" "$scratch/acquisition-summary.json"
command="go run github.com/onsi/ginkgo/v2/ginkgo --race --fail-on-empty --fail-on-pending --json-report=$scratch/acquisition.json ./pkg/embeddingmedia"
printf '%s\n' "$command" > "$scratch/commands.log"
go run github.com/onsi/ginkgo/v2/ginkgo --race --fail-on-empty --fail-on-pending \
 --json-report="$scratch/acquisition.json" ./pkg/embeddingmedia 2>&1 | tee "$scratch/acquisition.log"
MM_ACQUISITION_TEST_SCRATCH="$scratch" python3 -I -m unittest discover -s test/multimodal \
 -p test_acquisition_reports.py -v 2>&1 | tee "$scratch/checker-tests.log"
python3 -I test/multimodal/check_acquisition_reports.py "$scratch/acquisition.json" "$revision" "bash test/multimodal/verify.sh acquisition"
