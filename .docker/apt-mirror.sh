#!/bin/sh
# Reconfigure Ubuntu apt sources to point at an alternate mirror.
#
# Used by Dockerfiles via `RUN --mount=type=bind,source=.docker/apt-mirror.sh,...`
# and by CI workflows on the runner to mitigate outages of the default
# archive.ubuntu.com / security.ubuntu.com / ports.ubuntu.com pool.
#
# Inputs (env):
#   APT_MIRROR        Replacement for archive.ubuntu.com and security.ubuntu.com
#                     (e.g. "http://azure.archive.ubuntu.com" or
#                      "https://mirrors.edge.kernel.org").
#                     Leave empty to keep upstream. The trailing "/ubuntu/..."
#                     path is preserved by the rewrite.
#   APT_PORTS_MIRROR  Replacement for ports.ubuntu.com (arm64/ppc64el/...).
#                     Leave empty to keep upstream.
#
# Both default to empty, in which case the script is a no-op.

set -e

if [ -z "${APT_MIRROR}" ] && [ -z "${APT_PORTS_MIRROR}" ]; then
    exit 0
fi

# Ubuntu 24.04 (noble) ships DEB822 sources at /etc/apt/sources.list.d/ubuntu.sources;
# older releases use /etc/apt/sources.list. We rewrite whichever exists.
for f in /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list; do
    [ -f "$f" ] || continue
    if [ -n "${APT_MIRROR}" ]; then
        # Use a comma delimiter so the alternation pipe in the regex
        # is not interpreted as the s/// separator.
        sed -i -E "s,https?://(archive\.ubuntu\.com|security\.ubuntu\.com),${APT_MIRROR},g" "$f"
    fi
    if [ -n "${APT_PORTS_MIRROR}" ]; then
        sed -i -E "s,https?://ports\.ubuntu\.com,${APT_PORTS_MIRROR},g" "$f"
    fi
done

echo "apt-mirror: rewrote sources (APT_MIRROR='${APT_MIRROR}', APT_PORTS_MIRROR='${APT_PORTS_MIRROR}')"
