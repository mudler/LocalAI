#!/usr/bin/env sh
# summarize-ginkgo-waits.sh THRESHOLD_SECONDS ROOT LOG [LIMIT]
set -eu

threshold="${1:?missing threshold in seconds}"
root="${2:?missing test root}"
log="${3:?missing Ginkgo log}"
limit="${4:-25}"
case "$limit" in
	''|*[!0-9]*) echo "limit must be a non-negative integer" >&2; exit 2 ;;
esac

# Strip terminal colour sequences, then report slow specs and hooks. This
# catches sleeps, polling, channel waits, teardown and any other idle time
# without unsafe attempts to replace Go's process-wide clock primitives.
sed 's/\[[0-9;]*[[:alpha:]]//g' "$log" | awk -v threshold="$threshold" -v root="$root" '
	/\[[0-9]+([.][0-9]+)? seconds\]/ {
		line = $0
		sub(/^.*\[/, "", line)
		sub(/ seconds\].*$/, "", line)
		seconds = line + 0
		if (seconds < threshold) next
		description = "(description unavailable)"
		location = "(location unavailable)"
		if (getline > 0) description = $0
		if (getline > 0) location = $0
		printf "%010.3f\t%-18s\t%s\t%s\n", seconds, root, description, location
	}
' | sort -t "$(printf '\t')" -k1,1nr | sed -n "1,${limit}p" | awk -F '\t' '{ printf "  %7.3fs  %s  %s  %s\n", $1 + 0, $2, $3, $4 }'
