#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs every Tables suite in order, the acceptance last, and prints the final
# inventory:
#   suite -> passed count, skipped count, status
# It exits nonzero when any suite fails, reports a skipped case, leaves a
# process behind or is missing from the set, and it never stops at the first
# failure, so one run shows every broken suite.
#
# The set is explicit. A suite named tables-* that is added to
# scripts/addons/suites.json and not named here is reported as missing from the
# gate, so the gate cannot silently fall behind the inventory. The foundation
# suites are not part of this gate: scripts/addons/foundation_gate.sh runs them.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
mkdir -p "$cache/logs" "$cache/tmp"
. "$root/scripts/addons/leaks.sh"
suites='tables-databases tables-schema tables-rows tables-tools tables-admin tables-ui tables-csv tables-acceptance'
results="$cache/logs/tables-gate-$$.jsonl"
: >"$results"
for suite in $suites; do
    out="$cache/logs/tables-gate-$suite.out"
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
python3 -I - "$results" "$root/scripts/addons/suites.json" "$suites" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
inventory = json.load(open(sys.argv[2]))
named = sys.argv[3].split()
tables = sorted(k for k in inventory if k.startswith('tables-'))
missing = sorted(set(tables) - set(named))
absent = sorted(set(named) - set(inventory))
width = max(len(r['suite']) for r in rows)
print('tables gate inventory')
total = skipped = 0
bad = False
for r in rows:
    print('  %-*s  passed=%-4d skipped=%-2d %s' % (width, r['suite'], r['passed'], r['skipped'], r['status']))
    total += r['passed']
    skipped += r['skipped']
    if r['status'] != 'passed':
        bad = True
for name in missing:
    print('  %-*s  not part of the gate: add it to scripts/addons/tables_gate.sh' % (width, name))
for name in absent:
    print('  %-*s  named by the gate and absent from suites.json' % (width, name))
print('total passed=%d skipped=%d suites=%d' % (total, skipped, len(rows)))
sys.exit(1 if bad or missing or absent else 0)
PY
