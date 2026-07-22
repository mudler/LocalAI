#!/usr/bin/env sh
#
# run-coverage.sh OUTPUT_DIR MERGED_PROFILE FLAKE_ATTEMPTS UNIT_ROOT [UNIT_ROOT...]
#
# Runs the unit/suite tests under each UNIT_ROOT (recursively) plus the
# in-process integration suites in $COVERAGE_E2E_ROOTS, all instrumented to
# attribute coverage to $COVERAGE_COVERPKG, and merges everything into
# MERGED_PROFILE.
#
# Environment:
#   GINKGO_TAGS          build tags, e.g. "debug auth"
#   COVERAGE_COVERPKG    packages coverage is attributed to (comma-separated
#                        go patterns). Required so the in-process e2e suites
#                        credit the core/http handlers they drive over HTTP —
#                        with default per-package cover they'd only credit the
#                        e2e_test package itself.
#   COVERAGE_E2E_ROOTS   space-separated in-process integration suites, run
#                        NON-recursively (so tests/e2e/distributed, which needs
#                        containers, is excluded) with --label-filter.
#   COVERAGE_E2E_LABELS  ginkgo --label-filter for the e2e roots, e.g.
#                        "!real-models" (those specs need a downloaded model).
#   COVERAGE_EXCLUDE_RE  egrep pattern of profile lines to drop before merging,
#                        e.g. generated protobuf (grpc/proto/.*\.pb\.go).
#   COVERAGE_PROCS       parallel Ginkgo processes; 0 lets Ginkgo detect CPUs.
#   COVERAGE_SUITE_TIMEOUT maximum duration of each recursive root (default 5m).
#   COVERAGE_PROGRESS_AFTER emit diagnostics when a spec is slow (default 30s).
#
# Verbose Ginkgo output is retained in OUTPUT_DIR/logs. The previous run's log
# for each root is kept with a .previous suffix, so a noisy failure remains
# available without flooding the commit-hook output.
#
# Why one ginkgo invocation per root: passing several recursive roots to a
# single ginkgo run only merges ONE root's coverprofile into --output-dir
# (verified ginkgo 2.29.0) — the rest are silently dropped. So each root runs
# separately. Because --coverpkg makes the same package appear in multiple
# root profiles, the merge SUMS per-block hit counts (a plain concat would
# duplicate blocks and miscount); summing is valid for covermode=atomic.
#
# POSIX sh (no bash): no arrays. Roots are space-free tokens iterated with
# word-splitting; the only flag that can contain a space (--tags="debug auth")
# is held in the positional parameters ("$@") so its quoting survives.
set -u

out_dir="${1:?usage: run-coverage.sh OUTPUT_DIR MERGED_PROFILE FLAKES UNIT_ROOT...}"
merged="${2:?missing MERGED_PROFILE}"
flakes="${3:?missing FLAKE_ATTEMPTS}"
shift 3
unit_roots="$*" # space-free tokens (./pkg ./core)

mkdir -p "$out_dir"
lock_dir="$out_dir/.run-coverage.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
	echo "run-coverage: another coverage run is using $out_dir" >&2
	echo "run-coverage: wait for it to finish; if none is running, remove stale lock $lock_dir" >&2
	exit 2
fi
run_marker="$lock_dir/generated-after"
touch "$run_marker"
cleanup() {
	for root in $unit_roots ${COVERAGE_E2E_ROOTS:-}; do
		find "$root" -type f -name '*.test' -newer "$run_marker" -delete 2>/dev/null || :
	done
	rm -f "$run_marker"
	rmdir "$lock_dir" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

log_dir="$out_dir/logs"
mkdir -p "$log_dir"
# Clear per-root profiles from a previous run: the merge collects them by glob,
# so a stale profile (e.g. from a root that failed to rebuild this run) must not
# leak into the merged result.
rm -f "$out_dir"/cover-*.out
rm -f "$out_dir"/*_cover-*.out
rm -f "$merged"
fail=0

procs="${COVERAGE_PROCS:-0}"
suite_timeout="${COVERAGE_SUITE_TIMEOUT:-5m}"
progress_after="${COVERAGE_PROGRESS_AFTER:-30s}"
parallel_flags="-p --keep-going --timeout=$suite_timeout --poll-progress-after=$progress_after --poll-progress-interval=10s"
if [ "$procs" -gt 0 ] 2>/dev/null; then
	parallel_flags="$parallel_flags --procs=$procs --compilers=$procs"
fi

# Common optional flags go into "$@"; unquoted ${VAR:+...} would word-split a
# --tags value that contains a space. The unit roots were captured above, so
# overwriting the positional parameters here is safe.
set --
[ -n "${GINKGO_TAGS:-}" ] && set -- "$@" "--tags=$GINKGO_TAGS"
[ -n "${COVERAGE_COVERPKG:-}" ] && set -- "$@" "--coverpkg=$COVERAGE_COVERPKG"

# cover-<root>.out, with path separators flattened (POSIX BRE, no \+).
profile_name() {
	printf 'cover-%s.out' "$(printf '%s' "$1" | sed 's#[./][./]*#_#g; s#^_##; s#_$##')"
}

log_name() {
	printf '%s.log' "$(printf '%s' "$1" | sed 's#[./][./]*#_#g; s#^_##; s#_$##')"
}

rotate_log() {
	log="$1"
	if [ -f "$log" ]; then
		mv -f "$log" "$log.previous"
	fi
}

# Ginkgo's recursive-run merger can fail after every suite has passed when a
# large --coverpkg run produces many profiles. Keep its profiles separate and
# merge them here using the same block-summing rule as the cross-root merge.
consolidate_root_profiles() {
	base="$1"
	set -- "$out_dir"/*_"$base"
	if [ ! -e "$1" ]; then
		echo "run-coverage: no per-package profiles produced for $base" >&2
		return 1
	fi
	tmp="$out_dir/.${base}.tmp"
	{
		echo "mode: atomic"
		awk '
			/^mode:/ { next }
			{ stmts[$1] = $2; cnt[$1] += $3 }
			END { for (k in stmts) print k, stmts[k], cnt[k] }
		' "$@"
	} > "$tmp"
	mv "$tmp" "$out_dir/$base"
	rm -f "$@"
}

report_failure() {
	root="$1"
	log="$2"
	echo "run-coverage: FAIL — tests under coverage failed for $root" >&2
	echo "run-coverage: full output: $log" >&2
	echo "run-coverage: relevant tail:" >&2
	# Keep the terminal useful even when Ginkgo emits thousands of verbose lines.
	# The complete log remains available when this short extract is insufficient.
	summary="$(grep -E 'Summarizing|\[FAIL(ED)?\]|FAIL!|--- FAIL:|Test Suite Failed|could not finalize|Status code: 429|HTTP 429|rate limit|timed out|panic:|fork/exec|no such file or directory|Expected.*(but got|success)' "$log" \
		| tail -n 30)"
	if [ -n "$summary" ]; then
		printf '%s\n' "$summary" >&2
	else
		tail -n 30 "$log" >&2
	fi
}

# Unit/suite roots: recursive.
for root in $unit_roots; do
	base="$(profile_name "$root")"
	log="$log_dir/$(log_name "$root")"
	rotate_log "$log"
	echo "run-coverage: testing $root (full output: $log)"
	# parallel_flags is intentionally word-split: it contains CLI arguments only.
	# shellcheck disable=SC2086
	go run github.com/onsi/ginkgo/v2/ginkgo $parallel_flags --keep-separate-coverprofiles --flake-attempts "$flakes" -v -r "$@" \
		--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" >"$log" 2>&1 \
		&& consolidate_root_profiles "$base" \
		&& echo "run-coverage: PASS — $root" \
		|| { fail=1; report_failure "$root" "$log"; }
done

# In-process integration roots: NON-recursive + optional label filter.
for root in ${COVERAGE_E2E_ROOTS:-}; do
	base="$(profile_name "$root")"
	log="$log_dir/$(log_name "$root")"
	rotate_log "$log"
	echo "run-coverage: testing $root (full output: $log)"
	if [ -n "${COVERAGE_E2E_LABELS:-}" ]; then
		# shellcheck disable=SC2086
		go run github.com/onsi/ginkgo/v2/ginkgo $parallel_flags --keep-separate-coverprofiles --flake-attempts "$flakes" -v "$@" \
			--label-filter="$COVERAGE_E2E_LABELS" \
			--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" >"$log" 2>&1 \
			&& consolidate_root_profiles "$base" \
			&& echo "run-coverage: PASS — $root" \
			|| { fail=1; report_failure "$root" "$log"; }
	else
		# shellcheck disable=SC2086
		go run github.com/onsi/ginkgo/v2/ginkgo $parallel_flags --keep-separate-coverprofiles --flake-attempts "$flakes" -v "$@" \
			--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" >"$log" 2>&1 \
			&& consolidate_root_profiles "$base" \
			&& echo "run-coverage: PASS — $root" \
			|| { fail=1; report_failure "$root" "$log"; }
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "run-coverage: FAILED — one or more test suites failed; no merged profile was produced." >&2
	echo "run-coverage: the coverage percentage ratchet was not run." >&2
	exit "$fail"
fi

# Collect the per-root profiles by glob (space-safe, no list to track).
set -- "$out_dir"/cover-*.out
if [ ! -e "$1" ]; then
	echo "run-coverage: no coverprofiles produced" >&2
	exit 2
fi

# Merge: drop the per-file mode headers and any excluded (generated) lines,
# then sum hit counts per identical block, emitting a single mode header.
{
	echo "mode: atomic"
	awk -v exre="${COVERAGE_EXCLUDE_RE:-}" '
		/^mode:/ { next }
		exre != "" && $1 ~ exre { next }
		{ stmts[$1] = $2; cnt[$1] += $3 }
		END { for (k in stmts) print k, stmts[k], cnt[k] }
	' "$@"
} > "$merged"

echo "run-coverage: all test suites passed; merged profile: $merged"
