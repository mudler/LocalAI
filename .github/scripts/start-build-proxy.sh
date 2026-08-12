#!/usr/bin/env bash
set -euo pipefail
output="${RUNNER_TEMP}/localai-build-proxy"
mkdir -p "$output"
CGO_ENABLED=0 GOCACHE="${RUNNER_TEMP}/go-build-cache" go build -o "$output/build-proxy" ./cmd/build-proxy
nohup "$output/build-proxy" --listen 127.0.0.1:18080 --output "$output" >"$output/proxy.log" 2>&1 &
echo "$!" >"$output/proxy.pid"
for _ in $(seq 1 50); do
  grep -q '^ca=' "$output/proxy.log" && break
  sleep 0.1
done
grep '^proxy=' "$output/proxy.log"
grep '^ca=' "$output/proxy.log"
proxy_ca="$output/ca/ca.crt"
ca_bundle="$output/ca/ca-bundle.crt"
system_ca=""
for candidate in \
  /etc/ssl/certs/ca-certificates.crt \
  /etc/ssl/cert.pem \
  /etc/pki/tls/certs/ca-bundle.crt \
  /etc/openssl/certs/ca-certificates.crt; do
  if test -s "$candidate"; then
    system_ca="$candidate"
    break
  fi
done
if test -z "$system_ca"; then
  echo 'build proxy: unable to find the runner system CA bundle' >&2
  exit 1
fi
cat "$system_ca" "$proxy_ca" >"$ca_bundle"
{
  echo "LOCALAI_BUILD_PROXY=http://127.0.0.1:18080"
  echo "LOCALAI_BUILD_PROXY_OUTPUT=$output"
  echo "LOCALAI_BUILD_PROXY_CA=$proxy_ca"
  echo "LOCALAI_BUILD_PROXY_CA_BUNDLE=$ca_bundle"
  echo "HTTP_PROXY=http://127.0.0.1:18080"
  echo "HTTPS_PROXY=http://127.0.0.1:18080"
  echo "http_proxy=http://127.0.0.1:18080"
  echo "https_proxy=http://127.0.0.1:18080"
  echo "SSL_CERT_FILE=$ca_bundle"
  echo "CURL_CA_BUNDLE=$ca_bundle"
  echo "REQUESTS_CA_BUNDLE=$ca_bundle"
  echo "GIT_SSL_CAINFO=$ca_bundle"
  echo "NODE_EXTRA_CA_CERTS=$proxy_ca"
  echo "NO_PROXY=localhost,127.0.0.1"
  echo "no_proxy=localhost,127.0.0.1"
} >>"$GITHUB_ENV"
