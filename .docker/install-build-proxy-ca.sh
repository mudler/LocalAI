#!/bin/sh
# Install CI's optional HTTPS interception CA without replacing public roots.

set -e

proxy_ca=/run/secrets/build_proxy_ca
if [ ! -s "$proxy_ca" ]; then
    exit 0
fi

mkdir -p /usr/local/share/ca-certificates
cp "$proxy_ca" /usr/local/share/ca-certificates/localai-build-proxy.crt
update-ca-certificates
