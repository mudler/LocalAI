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
{
  echo "LOCALAI_BUILD_PROXY=http://127.0.0.1:18080"
  echo "LOCALAI_BUILD_PROXY_OUTPUT=$output"
  echo "LOCALAI_BUILD_PROXY_CA=$output/ca/ca.crt"
  echo "HTTP_PROXY=http://127.0.0.1:18080"
  echo "HTTPS_PROXY=http://127.0.0.1:18080"
  echo "http_proxy=http://127.0.0.1:18080"
  echo "https_proxy=http://127.0.0.1:18080"
  echo "SSL_CERT_FILE=$output/ca/ca.crt"
  echo "CURL_CA_BUNDLE=$output/ca/ca.crt"
  echo "REQUESTS_CA_BUNDLE=$output/ca/ca.crt"
  echo "GIT_SSL_CAINFO=$output/ca/ca.crt"
  echo "NODE_EXTRA_CA_CERTS=$output/ca/ca.crt"
  echo "NO_PROXY=localhost,127.0.0.1"
  echo "no_proxy=localhost,127.0.0.1"
} >>"$GITHUB_ENV"
