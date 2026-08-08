#!/bin/sh
# Reconfigure Ubuntu apt sources to point at an alternate mirror.
#
# Used by Dockerfiles via `RUN --mount=type=bind,source=.docker/apt-mirror.sh,...`
# and by CI workflows on the runner to mitigate outages of the default
# archive.ubuntu.com / security.ubuntu.com / ports.ubuntu.com pool.
#
# Inputs (env):
#   APT_MIRROR        Replacement for archive.ubuntu.com and security.ubuntu.com
#                     (e.g. "https://azure.archive.ubuntu.com").
#                     Leave empty to keep upstream. The trailing "/ubuntu/..."
#                     path is preserved by the rewrite.
#   APT_PORTS_MIRROR  Replacement for ports.ubuntu.com (arm64/ppc64el/...).
#                     Leave empty to keep upstream.
#
# Both default to empty, in which case the script is a no-op.

set -e

# BuildKit exposes the ephemeral interception CA at this dedicated path. Copy
# it into the image trust bundle only when the proxy-enabled workflows supply
# it; ordinary local and test builds retain their base-image trust unchanged.
proxy_ca=/run/secrets/build_proxy_ca
if [ -s "$proxy_ca" ]; then
    # Keep the generated CA in the distribution-managed local certificate
    # directory. Installing or upgrading ca-certificates later in this layer
    # regenerates the bundle, so appending directly to it would be lost.
    mkdir -p /usr/local/share/ca-certificates
    cp "$proxy_ca" /usr/local/share/ca-certificates/localai-build-proxy.crt
    if command -v update-ca-certificates >/dev/null 2>&1; then
        update-ca-certificates
    fi
    cat > /etc/apt/apt.conf.d/99localai-build-proxy-ca <<EOF
Acquire::https::CaInfo "$proxy_ca";
EOF
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

# Build networking is HTTPS-only. Upgrade any untouched distribution defaults
# as well (notably ports.ubuntu.com when a caller leaves its override empty).
for f in /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list; do
    [ -f "$f" ] || continue
    sed -i -E 's,http://,https://,g' "$f"
done

echo "apt-mirror: rewrote sources (APT_MIRROR='${APT_MIRROR}', APT_PORTS_MIRROR='${APT_PORTS_MIRROR}')"
