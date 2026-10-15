#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
if [[ $# != 1 || ( $1 != inventory && $1 != wire && $1 != routing && $1 != contracts && $1 != acquisition ) ]]; then
    echo 'usage: bash test/multimodal/verify.sh inventory|wire|routing|contracts|acquisition' >&2
    exit 2
fi
export PYTHONDONTWRITEBYTECODE=1
if [[ $1 == acquisition ]]; then
    exec bash test/multimodal/verify-acquisition.sh
fi
if [[ $1 == contracts ]]; then
    exec bash test/multimodal/verify-contracts.sh
fi
if [[ $1 == routing ]]; then
    exec bash test/multimodal/verify-routing.sh
fi
if [[ $1 == wire ]]; then
    exec bash test/multimodal/verify-wire.sh
fi
scratch=.sdd-work/task1
mkdir -p "$scratch"
revision=$(git rev-parse HEAD)
command="python3 -m unittest discover -s test/multimodal -p test_check_reports.py"
finish() {
    code=$?
    trap - EXIT
    printf 'command=%s\nrevision=%s\nexit_status=%s\nevidence=%s\n' \
        "$command; python3 test/multimodal/check_reports.py inventory" "$revision" "$code" \
        "$scratch/unittest.log $scratch/checker.log $scratch/inventory-report.json" > "$scratch/verify.log"
    exit "$code"
}
trap finish EXIT
# Counts in the runner output come from unittest, not the planned native matrix.
python3 -m unittest discover -s test/multimodal -p 'test_check_reports.py' -v 2>&1 | tee "$scratch/unittest.log"
python3 test/multimodal/check_reports.py inventory | tee "$scratch/checker.log"
python3 - "$revision" "$scratch" <<'PY'
import json
from pathlib import Path
import re
import sys
revision, scratch = sys.argv[1:]
log = (Path(scratch) / 'unittest.log').read_text()
match = re.search(r'Ran (\d+) tests?', log)
if not match or int(match[1]) < 5 or not log.rstrip().endswith('OK'):
    raise SystemExit('missing, skipped or failed checker tests')
report = {
    'status': 'PENDING', 'blockers': ['native build/runtime approval and artifacts absent'],
    'command': 'python3 test/multimodal/check_reports.py inventory',
    'revision': revision, 'exit_status': 0,
    'counts': {'expected': 1, 'executed': 1, 'passed': 1, 'skipped': 0, 'pending': 0},
    'unit_test_counts': {'executed': int(match[1]), 'passed': int(match[1]), 'skipped': 0},
    'cases': [{'id': 'inventory', 'status': 'PASS'}],
    'evidence_paths': [scratch + '/unittest.log', scratch + '/checker.log'],
}
(Path(scratch) / 'inventory-report.json').write_text(json.dumps(report, indent=2) + '\n')
PY
python3 test/multimodal/check_reports.py inventory --report "$scratch/inventory-report.json"
