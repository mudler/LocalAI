#!/usr/bin/env bash
set -euo pipefail
output="${LOCALAI_BUILD_PROXY_OUTPUT:?build proxy output is unset}"
if test -f "$output/proxy.pid"; then
  kill -TERM "$(cat "$output/proxy.pid")" 2>/dev/null || true
  for _ in $(seq 1 50); do
    test -f "$output/summary.json" && break
    sleep 0.1
  done
fi
if test -f "$output/summary.json"; then
  {
    echo '### Build network inventory'
    echo
    echo 'HTTPS without the generated CA is reported as CONNECT because its HTTP method is encrypted.'
    echo
    echo '```json'
    cat "$output/summary.json"
    echo '```'
  } >>"$GITHUB_STEP_SUMMARY"
fi
