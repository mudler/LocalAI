#!/bin/bash
# Shared curl wrapper for the nightly dependency-bump scripts.
#
# The bump workflow fans out to ~25 parallel matrix jobs, each querying
# api.github.com. Anonymous API calls are capped at 60/hour per source IP and
# GitHub-hosted runners egress through shared NAT addresses, so a random handful
# of jobs were getting rate-limited (HTTP 403 -> curl exit 22, empty response)
# every single night. Authenticating with GITHUB_TOKEN lifts the ceiling to
# 1000/hour; the retries absorb whatever transient blips remain.

# Wraps curl with GitHub auth (when a token is present) plus retry/timeout
# hardening. Callers pass their own headers and the URL.
gh_curl() {
    # The bump scripts run under `set -x`; without this the Authorization header
    # would be echoed into the job log on every call.
    local had_xtrace=0
    case "$-" in
        *x*) had_xtrace=1; set +x ;;
    esac

    local args=(
        --silent --show-error --location --fail
        # --retry-all-errors so 403 rate-limit responses are retried too; plain
        # --retry only covers 408/429/5xx. curl honours Retry-After when sent.
        --retry 5 --retry-delay 3 --retry-all-errors
        --connect-timeout 15 --max-time 60
    )
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        args+=(--header "Authorization: Bearer ${GITHUB_TOKEN}")
    fi

    curl "${args[@]}" "$@"
    local rc=$?

    if [ "$had_xtrace" -eq 1 ]; then
        set -x
    fi
    return $rc
}
