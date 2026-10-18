#!/usr/bin/env bash
# shellcheck disable=SC2312
#
# run-coverage.sh — run ginkgo per test root, merge coverprofiles, and
# enforce a coverage floor.  Designed for POSIX sh (dash) and bash.
#
# Usage:
#   run-coverage.sh OUTPUT_DIR MERGED_PROFILE FLAKE_ATTEMPTS UNIT_ROOT...
#
# Environment:
#   GINKGO_TAGS         Space-separated build tags (e.g. "debug auth").
#   COVERAGE_COVERPKG   Comma-separated --coverpkg pattern.
#   COVERAGE_E2E_ROOTS  Space-separated non-recursive e2e roots.
#   COVERAGE_E2E_LABELS Ginkgo --label-filter for e2e roots.
#   COVERAGE_EXCLUDE_RE Regex for coverprofile lines to drop on merge.
#
# Each ginkgo invocation's combined stdout+stderr is captured to a per-root
# log file (OUTPUT_DIR/<root>.log).  On failure the last 80 lines are
# printed to stderr so the error is visible under `make --output-sync=target`
# (which buffers all output and flushes at the end, hiding the actual error
# in interleaved output).  On success the log is replayed to stdout.

set -u

out_dir="${1:?usage: run-coverage.sh OUTPUT_DIR MERGED_PROFILE FLAKES UNIT_ROOT...}"
merged="${2:?missing MERGED_PROFILE}"
flakes="${3:?missing FLAKE_ATTEMPTS}"
shift 3
unit_roots="$*"

fail=0

# Build flags shared by every ginkgo invocation.  We store them as a
# newline-delimited list in $build_flags_list so we can iterate without
# word-splitting issues (e.g. --tags="debug auth" contains a space).
build_flags_list=""
if [ -n "${GINKGO_TAGS:-}" ]; then
	build_flags_list="${build_flags_list}${build_flags_list:+
}--tags=$GINKGO_TAGS"
fi
if [ -n "${COVERAGE_COVERPKG:-}" ]; then
	build_flags_list="${build_flags_list}${build_flags_list:+
}--coverpkg=$COVERAGE_COVERPKG"
fi

# cover-<root>.out, with path separators flattened (POSIX BRE, no \+).
profile_name() {
	printf 'cover-%s.out' "$(printf '%s' "$1" | sed 's#[./][./]*#_#g; s#^_##; s#_$##')"
}

# run_ginkgo ROOT RECURSIVE [LABEL_FILTER]
# Runs ginkgo for ROOT, captures combined output to a log file, and on
# failure prints the tail to stderr.
run_ginkgo() {
	_root="$1"
	_recursive="$2"
	_label_filter="${3:-}"
	_base="$(profile_name "$_root")"
	_log="$out_dir/$_base.log"

	if [ "$_recursive" = "1" ]; then
		_rflag="-r"
	else
		_rflag=""
	fi

	# Build the ginkgo command.  We use eval to properly handle --tags
	# values that contain spaces.  The build_flags_list is newline-delimited;
	# we convert newlines to spaces for the eval string.  Each flag is
	# already in the form --key=value, and the value never contains shell
	# metacharacters (tags are alphanum, coverpkg is a Go pattern).
	_cmd="go run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $flakes -v $_rflag"
	# Append build flags (one per line -> one per arg)
	if [ -n "$build_flags_list" ]; then
		_bf=$(printf '%s' "$build_flags_list" | while IFS= read -r _line; do
			# Quote the value part after = so spaces are preserved
			printf '%s ' "${_line%%=*}=\"${_line#*=}\""
		done)
		_cmd="$_cmd $_bf"
	fi
	if [ -n "$_label_filter" ]; then
		_cmd="$_cmd --label-filter=$_label_filter"
	fi
	_cmd="$_cmd --cover --covermode=atomic --coverprofile=$_base --output-dir=$out_dir $_root"

	# Run ginkgo, capture all output to the log file.
	eval "$_cmd" >"$_log" 2>&1
	_rc=$?
	if [ "$_rc" -ne 0 ]; then
		echo "run-coverage: ginkgo failed for $_root (exit $_rc), last 80 lines:" >&2
		tail -80 "$_log" >&2
		fail=1
	else
		# Replay the log so test output is visible in CI.
		cat "$_log"
	fi
	return "$_rc"
}

# Unit/suite roots: recursive.
for root in $unit_roots; do
	run_ginkgo "$root" 1 || true
done

# In-process integration roots: NON-recursive + optional label filter.
for root in ${COVERAGE_E2E_ROOTS:-}; do
	run_ginkgo "$root" 0 "${COVERAGE_E2E_LABELS:-}" || true
done

#########################################################
## Merge coverprofiles
#########################################################

# Collect all coverprofiles except the merged one.
profiles=""
for f in "$out_dir"/cover-*.out; do
	[ -f "$f" ] || continue
	[ "$f" = "$merged" ] && continue
	if [ -z "$profiles" ]; then
		profiles="$f"
	else
		profiles="$profiles $f"
	fi
done

if [ -z "$profiles" ]; then
	echo "run-coverage: no coverprofiles found in $out_dir" >&2
	exit 1
fi

# Merge: drop the mode line from all but the first profile, optionally
# exclude generated proto files, then concatenate.
first=1
for f in $profiles; do
	if [ "$first" = "1" ]; then
		cat "$f"
		first=0
	else
		tail -n +2 "$f"
	fi
done | { if [ -n "${COVERAGE_EXCLUDE_RE:-}" ]; then
	grep -vE "$COVERAGE_EXCLUDE_RE"
else
	cat
fi; } >"$merged"

echo "run-coverage: merged $(echo "$profiles" | wc -w) coverprofiles into $merged"

exit "$fail"
