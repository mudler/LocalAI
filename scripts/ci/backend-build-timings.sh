#!/usr/bin/env bash
# Print the slowest backend image jobs of a workflow run and fail when a
# llama.cpp-family build came close to GitHub's 6h job limit.
#
# Hosted runners kill a job at 6h. The ROCm llama.cpp builds once took 5h11m on
# master and hit the limit on a PR, and nothing reported the drift until a job
# died. This check makes the margin visible: it writes a table of the slowest
# jobs to the step summary and fails when a llama.cpp-family job (llama-cpp,
# ik-llama-cpp, turboquant, bonsai, olala) ran longer than LIMIT_MINUTES.
#
# Usage: backend-build-timings.sh <run-id> [attempt]
# Env:   GH_TOKEN, GITHUB_REPOSITORY (owner/name), LIMIT_MINUTES (default 240),
#        TOP (default 20), GITHUB_STEP_SUMMARY (optional),
#        JOBS_JSON (optional: a file with the jobs array, for tests).
set -euo pipefail

RUN_ID=${1:?run id is required}
ATTEMPT=${2:-1}
LIMIT_MINUTES=${LIMIT_MINUTES:-240}
TOP=${TOP:-20}
SUMMARY=${GITHUB_STEP_SUMMARY:-/dev/stdout}
FAMILY_RE='-(llama-cpp|ik-llama-cpp|turboquant|bonsai|olala)[,) ]'

if [ -n "${JOBS_JSON:-}" ]; then
  jobs=$(cat "$JOBS_JSON")
else
  jobs=$(gh api --paginate \
    "repos/${GITHUB_REPOSITORY}/actions/runs/${RUN_ID}/attempts/${ATTEMPT}/jobs?per_page=100" \
    --jq '.jobs[]' | jq -s '.')
fi

# Finished build jobs only, with their duration in minutes.
rows=$(jq -c '[.[]
  | select(.started_at != null and .completed_at != null)
  | select(.conclusion != "skipped")
  | select(.name | test("backend-jobs-(single|multi)arch"))
  | {name, conclusion,
     minutes: ((((.completed_at | fromdate) - (.started_at | fromdate)) / 60) | floor)}]
  | sort_by(-.minutes)' <<< "$jobs")

{
  echo "### Slowest backend image jobs (limit for llama.cpp family: ${LIMIT_MINUTES} min)"
  echo
  echo "| minutes | result | job |"
  echo "|---:|---|---|"
  jq -r --argjson top "$TOP" '.[:$top][] | "| \(.minutes) | \(.conclusion) | \(.name) |"' <<< "$rows"
} >> "$SUMMARY"

over=$(jq -r --arg re "$FAMILY_RE" --argjson limit "$LIMIT_MINUTES" \
  '.[] | select((.name | test($re)) and .minutes > $limit) | "\(.minutes) min: \(.name)"' <<< "$rows")

if [ -n "$over" ]; then
  echo "llama.cpp-family builds over ${LIMIT_MINUTES} min (GitHub kills a job at 360):" >&2
  echo "$over" >&2
  exit 1
fi
echo "No llama.cpp-family build over ${LIMIT_MINUTES} min."
