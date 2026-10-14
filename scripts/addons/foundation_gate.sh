#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs every foundation suite in order and prints the final inventory:
#   suite -> passed count, skipped count, status
# It exits nonzero when any suite fails or reports a skipped case, and it never
# stops at the first failure, so one run shows every broken suite.
#
# The set is explicit. A suite that is added to scripts/addons/suites.json and
# not named here is reported as missing from the gate, so the gate cannot
# silently shrink or fall behind the inventory.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
mkdir -p "$cache/logs" "$cache/tmp"
. "$root/scripts/addons/leaks.sh"
suites='prerequisites contracts catalog packages lifecycle runtime gateway policy proposals artifacts apps gallery-ui esm-ui host-slots recovery updates foundation-acceptance packaging'
# Tables suites are not foundation suites: scripts/addons/tables_gate.sh runs
# them. They are named here only so the inventory check does not report them
# as missing from the foundation gate.
tables_suites='tables-databases tables-schema tables-rows tables-tools tables-admin tables-ui tables-csv tables-acceptance'
results="$cache/logs/foundation-gate-$$.jsonl"
: >"$results"
for suite in $suites; do
    out="$cache/logs/foundation-gate-$suite.out"
    # A suite that leaves a process that names the scratch directory behind
    # fails, whatever its own result was; the leftover is killed.
    leak=0
    ok=0
    sh scripts/addons/verify.sh "$suite" >"$out" 2>&1 || ok=1
    reap_leaks "$cache/tmp/" "$suite" paths-only >>"$out" 2>&1 || leak=1
    if [ "$leak" -eq 1 ]; then
        printf '{"suite":"%s","passed":0,"skipped":0,"status":"leaked-processes","log":"%s"}\n' "$suite" "$out" >>"$results"
    elif [ "$ok" -eq 0 ]; then
        line=$(tail -n 1 "$out")
        printf '%s\n' "$line" | python3 -I -c 'import json,sys; d=json.loads(sys.stdin.read()); assert d["suite"]=="'"$suite"'"; print(json.dumps({"suite":d["suite"],"passed":d["passed"],"skipped":d["skipped"],"status":"passed" if d["skipped"]==0 else "skipped"}))' >>"$results" 2>/dev/null \
            || printf '{"suite":"%s","passed":0,"skipped":0,"status":"no-result"}\n' "$suite" >>"$results"
    else
        printf '{"suite":"%s","passed":0,"skipped":0,"status":"failed","log":"%s"}\n' "$suite" "$out" >>"$results"
    fi
done
python3 -I - "$results" "$root/scripts/addons/suites.json" "$suites" "$tables_suites" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
inventory = json.load(open(sys.argv[2]))
named = sys.argv[3].split()
tables = sys.argv[4].split()
missing = sorted(set(inventory) - set(named) - set(tables))
absent = sorted(set(named) - set(inventory))
width = max(len(r['suite']) for r in rows)
print('foundation gate inventory')
total = skipped = 0
bad = False
for r in rows:
    print('  %-*s  passed=%-4d skipped=%-2d %s' % (width, r['suite'], r['passed'], r['skipped'], r['status']))
    total += r['passed']
    skipped += r['skipped']
    if r['status'] != 'passed':
        bad = True
for name in missing:
    print('  %-*s  not part of the gate: add it to scripts/addons/foundation_gate.sh' % (width, name))
for name in absent:
    print('  %-*s  named by the gate and absent from suites.json' % (width, name))
print('total passed=%d skipped=%d suites=%d' % (total, skipped, len(rows)))
sys.exit(1 if bad or missing or absent else 0)
PY
