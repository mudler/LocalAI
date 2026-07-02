#!/usr/bin/env sh
#
# ui-coverage-check.sh SUMMARY_JSON BASELINE_FILE
#
# Compares the total line coverage in an nyc coverage-summary.json against a
# committed baseline and fails (exit 1) if it dropped by more than
# UI_COVERAGE_TOLERANCE percentage points (default 0.8). The React UI e2e suite
# drives the real app, so a removed feature or deleted spec shows up as a
# coverage drop here.
#
# Why the band is this wide: UI e2e line coverage is NOT deterministic. Many
# specs assert on state and end while async/lazy render work is still in flight,
# so those lines are collected only when the render beats the coverage teardown
# — and that depends on machine speed/load. The effect is diffuse (spread across
# dozens of specs, no single dominant file) and tracks the runner: a quiet local
# box measures ~0.9pp higher than a slow/loaded CI runner for the SAME tree
# (observed: 39.9% local vs 39.0% CI). The tolerance absorbs that spread; setting
# it tighter (it was briefly 0.1pp, calibrated to a lucky fast-local cluster)
# makes CI flap.
#
# The principled way to tighten this is to remove the variance at the source —
# make each racing spec await a rendered element before ending (e2e/agents.spec.js
# → AgentCreate fixed the single biggest one) — NOT to chase the baseline up to a
# fast-machine high or loosen further. Keep the baseline conservatively at or
# below the slow-runner floor so the band catches real regressions, not jitter.
#
# When coverage rises meaningfully AND reproducibly (check on a slow/CI-like run),
# regenerate and commit the baseline with:  make test-ui-coverage-baseline
set -eu

summary="${1:?usage: ui-coverage-check.sh SUMMARY_JSON BASELINE_FILE}"
baseline_file="${2:?usage: ui-coverage-check.sh SUMMARY_JSON BASELINE_FILE}"
tolerance="${UI_COVERAGE_TOLERANCE:-0.8}"

if [ ! -f "$summary" ]; then
	echo "ui-coverage-check: coverage summary not found: $summary" >&2
	echo "ui-coverage-check: run 'make test-ui-coverage' first." >&2
	exit 2
fi
if [ ! -f "$baseline_file" ]; then
	echo "ui-coverage-check: baseline not found: $baseline_file" >&2
	echo "ui-coverage-check: create it with 'make test-ui-coverage-baseline'." >&2
	exit 2
fi

current="$(node -e 'const fs=require("fs");process.stdout.write(String(JSON.parse(fs.readFileSync(process.argv[1])).total.lines.pct))' "$summary")"
baseline="$(tr -d '[:space:]%' < "$baseline_file")"

if [ -z "$current" ]; then
	echo "ui-coverage-check: could not parse total.lines.pct from $summary" >&2
	exit 2
fi
# Fail closed on a missing/garbage baseline rather than letting awk coerce an
# empty or non-numeric value to 0 (which would pass any coverage silently).
case "$baseline" in
	'' | *[!0-9.]* )
		echo "ui-coverage-check: baseline is empty or non-numeric ('$baseline') in $baseline_file" >&2
		echo "ui-coverage-check: regenerate it with 'make test-ui-coverage-baseline'" >&2
		exit 2 ;;
esac

if awk -v c="$current" -v b="$baseline" -v t="$tolerance" 'BEGIN { exit !(c < b - t) }'; then
	echo "ui-coverage-check: FAIL — UI line coverage ${current}% is below baseline ${baseline}% by more than ${tolerance}pp." >&2
	echo "ui-coverage-check: add or restore e2e specs; coverage regressed beyond the jitter tolerance." >&2
	exit 1
fi

if awk -v c="$current" -v b="$baseline" 'BEGIN { exit !(c > b) }'; then
	echo "ui-coverage-check: OK — UI line coverage rose to ${current}% (baseline ${baseline}%); consider 'make test-ui-coverage-baseline'."
else
	echo "ui-coverage-check: OK — UI line coverage ${current}% within ${tolerance}pp of baseline ${baseline}%."
fi
