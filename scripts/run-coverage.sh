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
# Clear per-root profiles from a previous run: the merge collects them by glob,
# so a stale profile (e.g. from a root that failed to rebuild this run) must not
# leak into the merged result.
rm -f "$out_dir"/cover-*.out
fail=0

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

# Unit/suite roots: recursive.
for root in $unit_roots; do
	base="$(profile_name "$root")"
	go run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts "$flakes" -v -r "$@" \
		--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" || fail=1
done

# In-process integration roots: NON-recursive + optional label filter.
for root in ${COVERAGE_E2E_ROOTS:-}; do
	base="$(profile_name "$root")"
	if [ -n "${COVERAGE_E2E_LABELS:-}" ]; then
		go run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts "$flakes" -v "$@" \
			--label-filter="$COVERAGE_E2E_LABELS" \
			--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" || fail=1
	else
		go run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts "$flakes" -v "$@" \
			--cover --covermode=atomic --coverprofile="$base" --output-dir="$out_dir" "$root" || fail=1
	fi
done

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

exit "$fail"
