#!/usr/bin/env bash
set -euo pipefail
if test -z "${LOCALAI_BUILD_PROXY_OUTPUT:-}"; then
  echo 'Build proxy did not start; skipping inventory finalization'
  exit 0
fi
output="$LOCALAI_BUILD_PROXY_OUTPUT"
if test -f "$output/proxy.pid"; then
  kill -TERM "$(cat "$output/proxy.pid")" 2>/dev/null || true
  for _ in $(seq 1 50); do
    test -f "$output/summary.json" && break
    sleep 0.1
  done
fi

# Later artifact uploads and action post-hooks must not target a stopped proxy.
{
  echo 'HTTP_PROXY='
  echo 'HTTPS_PROXY='
  echo 'http_proxy='
  echo 'https_proxy='
  echo 'SSL_CERT_FILE='
  echo 'CURL_CA_BUNDLE='
  echo 'REQUESTS_CA_BUNDLE='
  echo 'GIT_SSL_CAINFO='
  echo 'NODE_EXTRA_CA_CERTS='
} >>"$GITHUB_ENV"
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

# A matrix cancellation can interrupt checkout or a BuildKit request at any
# point. Preserve whatever inventory exists, but do not replace the canceled
# conclusion with a misleading proxy-enforcement failure.
if test "${LOCALAI_BUILD_JOB_STATUS:-}" = cancelled; then
  echo 'Build was cancelled; skipping network inventory enforcement'
  exit 0
fi

if ! test -s "$output/events.jsonl"; then
  echo 'Build proxy produced no network inventory' >&2
  exit 1
fi
if grep -qE '"method":"CONNECT"|"error":"plain HTTP is forbidden"' "$output/events.jsonl"; then
  echo 'Build traffic bypassed HTTPS interception or attempted plain HTTP' >&2
  exit 1
fi
