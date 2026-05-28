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
# UI e2e line coverage is NOT deterministic: async/debounced paths (e.g. the
# VRAM estimate's 500ms debounce) mean identical specs vary run-to-run. With the
# V8 path's single-chunk coverage build (vite.config.js inlineDynamicImports)
# the observed wobble is ~0.5pp, similar to the old istanbul path. The tolerance
# absorbs that jitter — keep it just above the observed wobble so a real ~1pp
# regression still trips the gate.
# (The Go gate carries a smaller tolerance for the same reason — its e2e slice.)
#
# When coverage rises meaningfully, regenerate and commit the baseline with:
#   make test-ui-coverage-baseline
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
