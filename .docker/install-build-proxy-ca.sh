#!/bin/sh
# Install CI's optional HTTPS interception CA without replacing public roots.

set -e

proxy_ca=/run/secrets/build_proxy_ca
if [ ! -s "$proxy_ca" ]; then
    exit 0
fi

mkdir -p /usr/local/share/ca-certificates
cp "$proxy_ca" /usr/local/share/ca-certificates/localai-build-proxy.crt
if command -v update-ca-certificates >/dev/null 2>&1; then
    update-ca-certificates
elif [ -f /etc/ssl/certs/ca-certificates.crt ]; then
    cat "$proxy_ca" >>/etc/ssl/certs/ca-certificates.crt
else
    echo 'build proxy: no system CA bundle found' >&2
    exit 1
fi

if [ -d /etc/apt/apt.conf.d ]; then
    cat > /etc/apt/apt.conf.d/99localai-build-proxy-ca <<EOF
Acquire::https::CaInfo "$proxy_ca";
EOF
fi
