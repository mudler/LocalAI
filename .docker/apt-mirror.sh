#!/bin/sh
# Reconfigure Ubuntu apt sources to point at an alternate mirror, and fall back
# to the upstream archive when that mirror turns out to be bad.
#
# Used by Dockerfiles via `RUN --mount=type=bind,source=.docker/apt-mirror.sh,...`
# and by CI workflows on the runner to mitigate outages of the default
# archive.ubuntu.com / security.ubuntu.com / ports.ubuntu.com pool.
#
# A mirror can fail in two ways: it can be slow or down, or it can be only
# partly synced, so that its index lists package versions whose files it does
# not have yet (apt then fails with "404 Not Found" on the .deb). Neither
# shows up until apt runs, so the rewrite alone is not enough.
#
# Usage:
#   apt-mirror.sh                  Rewrite the sources to the mirror(s) in the
#                                  env. Saves the original sources first, once.
#   apt-mirror.sh restore          Put the original (upstream) sources back.
#   apt-mirror.sh apt-get ARGS...  Run `apt-get ARGS...`. If it fails and the
#                                  sources were rewritten, print which mirror
#                                  failed, restore the upstream sources, run
#                                  `apt-get update`, and run the command once
#                                  more. The exit status is the one of the last
#                                  attempt. When the first attempt works, or
#                                  nothing was rewritten, this is the same as
#                                  running `apt-get ARGS...` directly.
#
# Wrap the apt-get calls that read the Ubuntu archive, one call each, e.g.
#   sh /usr/local/sbin/apt-mirror apt-get update
#   sh /usr/local/sbin/apt-mirror apt-get install -y --no-install-recommends git
# Once a call has fallen back, later calls in the same RUN run on upstream and
# are no longer retried. The retry runs in the same RUN, so it adds no layer.
#
# Inputs (env, for the rewrite only):
#   APT_MIRROR        Replacement for archive.ubuntu.com and security.ubuntu.com
#                     (e.g. "http://azure.archive.ubuntu.com" or
#                      "https://mirrors.edge.kernel.org").
#                     Leave empty to keep upstream. The trailing "/ubuntu/..."
#                     path is preserved by the rewrite.
#   APT_PORTS_MIRROR  Replacement for ports.ubuntu.com (arm64/ppc64el/...).
#                     Leave empty to keep upstream.
#
# Both default to empty, in which case the rewrite is a no-op and there is no
# fallback (upstream is already the only source).
#
# State: the original sources and the mirror names are kept in
# /var/backups/apt-mirror (a few KB). A source file that the rewrite did not
# change (for example a prebuilt base image that was rewritten by an earlier
# run of an older version of this script) is not saved, so it has no fallback.

set -e

BACKUP_DIR=/var/backups/apt-mirror
SOURCES="/etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list"

restore_sources() {
    [ -d "${BACKUP_DIR}/files" ] || return 0
    for b in "${BACKUP_DIR}"/files/*; do
        case "$b" in *.path) continue ;; esac
        [ -f "$b" ] || continue
        # The file name in the backup is the path with "/" turned into "_".
        orig=$(cat "${b}.path")
        cp "$b" "$orig"
    done
    rm -rf "${BACKUP_DIR}"
}

case "${1:-}" in
restore)
    restore_sources
    echo "apt-mirror: restored upstream sources"
    exit 0
    ;;
apt-get)
    "$@" && exit 0
    rc=$?
    if [ ! -f "${BACKUP_DIR}/mirror" ]; then
        exit "$rc"
    fi
    echo "apt-mirror: '$*' failed (exit ${rc}) with mirror $(cat "${BACKUP_DIR}/mirror"); restoring upstream sources and retrying once" >&2
    restore_sources
    # The command may have failed because the mirror index and pool disagree,
    # so refresh the lists from upstream before the retry (unless the failed
    # command was the update itself).
    if [ "${2:-}" != "update" ]; then
        apt-get update || exit $?
    fi
    exec "$@"
    ;;
"")
    ;;
*)
    echo "apt-mirror: unknown command '$1' (use: restore | apt-get ARGS...)" >&2
    exit 2
    ;;
esac

if [ -z "${APT_MIRROR}" ] && [ -z "${APT_PORTS_MIRROR}" ]; then
    exit 0
fi

# Ubuntu 24.04 (noble) ships DEB822 sources at /etc/apt/sources.list.d/ubuntu.sources;
# older releases use /etc/apt/sources.list. We rewrite whichever exists.
for f in $SOURCES; do
    [ -f "$f" ] || continue
    tmp="${f}.apt-mirror.new"
    cp "$f" "$tmp"
    if [ -n "${APT_MIRROR}" ]; then
        # Use a comma delimiter so the alternation pipe in the regex
        # is not interpreted as the s/// separator.
        sed -i -E "s,https?://(archive\.ubuntu\.com|security\.ubuntu\.com),${APT_MIRROR},g" "$tmp"
    fi
    if [ -n "${APT_PORTS_MIRROR}" ]; then
        sed -i -E "s,https?://ports\.ubuntu\.com,${APT_PORTS_MIRROR},g" "$tmp"
    fi
    if cmp -s "$f" "$tmp"; then
        rm -f "$tmp"
        continue
    fi
    # Keep the first (upstream) copy only; a second run sees rewritten sources.
    if [ ! -f "${BACKUP_DIR}/files/$(echo "$f" | tr / _)" ]; then
        mkdir -p "${BACKUP_DIR}/files"
        n="${BACKUP_DIR}/files/$(echo "$f" | tr / _)"
        cp "$f" "$n"
        echo "$f" > "${n}.path"
        echo "'${APT_MIRROR}' (ports '${APT_PORTS_MIRROR}')" > "${BACKUP_DIR}/mirror"
    fi
    cat "$tmp" > "$f"
    rm -f "$tmp"
done

echo "apt-mirror: rewrote sources (APT_MIRROR='${APT_MIRROR}', APT_PORTS_MIRROR='${APT_PORTS_MIRROR}')"
