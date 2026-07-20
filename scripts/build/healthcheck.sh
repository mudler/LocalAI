#!/bin/bash
# Docker HEALTHCHECK command for the LocalAI image.
#
# The image is a single artifact that runs several different processes, and they
# do not all serve HTTP on the same port — or at all. A hardcoded
# `curl -f http://localhost:8080/readyz` therefore reported every `local-ai
# worker` container as permanently `unhealthy` (issue #10987), which is worse
# than no healthcheck: a genuinely broken worker and a perfectly good one both
# read `unhealthy`, so the signal carries no information.
#
# The endpoint is derived from the mode the container is actually running plus
# the same env vars that configure the bind address, so a frontend moved off
# 8080 and a worker on a non-default base port are both probed correctly.
#
# Precedence:
#   1. HEALTHCHECK_ENDPOINT, if set — the documented escape hatch, and what
#      docker-compose.distributed.yaml has been shipping as a workaround.
#   2. Derived from the running mode.
#   3. The frontend endpoint, when the mode cannot be determined.
#
# Ports are read from environment variables only, which is how containers are
# configured in practice (compose/k8s set LOCALAI_ADDRESS, LOCALAI_SERVE_ADDR,
# ...). If you instead pass the bind address as a CLI flag, set
# HEALTHCHECK_ENDPOINT to match.
set -u

# Detect the arguments local-ai was started with. PID 1 is the usual case
# (entrypoint.sh exec's local-ai, so it inherits PID 1), but `init: true` puts
# docker-init there instead, so fall back to scanning /proc. Reading /proc
# directly avoids depending on ps/pgrep being installed in every image variant.
detect_argv() {
    if [ -n "${LOCALAI_HEALTHCHECK_ARGV:-}" ]; then
        printf '%s' "$LOCALAI_HEALTHCHECK_ARGV"
        return
    fi

    local cmdline proc pid d
    # LOCALAI_HEALTHCHECK_PROC exists so the regression test can point this at a
    # fixture tree; nothing in the image sets it.
    local procfs="${LOCALAI_HEALTHCHECK_PROC:-/proc}"
    # PID 1 first: entrypoint.sh exec's local-ai, so it normally *is* PID 1.
    # Only when something else holds PID 1 (`init: true` puts docker-init there)
    # do we scan, lowest PID first so the answer is deterministic if a container
    # somehow has more than one local-ai process. The healthcheck's own shell is
    # skipped: its argv mentions the script path, not a mode.
    # Sort on the PID itself rather than the whole path, so 7 comes before 64.
    for pid in 1 $(for d in "$procfs"/[0-9]*; do basename "$d"; done | sort -n); do
        proc="$procfs/$pid"
        [ -r "$proc/cmdline" ] || continue
        cmdline=$(tr '\0' ' ' < "$proc/cmdline" 2>/dev/null) || continue
        case "$cmdline" in
            *healthcheck.sh*) continue ;;
            *local-ai*)
                printf '%s' "$cmdline"
                return
                ;;
        esac
    done
}

# Extract the port from a bind address. Accepts ":8080", "0.0.0.0:8080" and
# "host:8080"; prints nothing when there is no port to find.
port_of() {
    case "$1" in
        *:*) printf '%s' "${1##*:}" ;;
    esac
}

# The mode is the first non-flag word after the local-ai binary.
#
# `run` is kong's default command, and it is declared `default:"withargs"` — so
# `local-ai gemma-4 whisper` is the *frontend* with two model arguments, not a
# command called "gemma-4". Unrecognised words must therefore fall through to
# the frontend; treating them as an unknown mode would silently stop probing the
# single most common invocation in the docs.
detect_mode() {
    local seen_binary=0 word
    for word in $1; do
        if [ "$seen_binary" = 0 ]; then
            case "$word" in
                *local-ai) seen_binary=1 ;;
            esac
            continue
        fi
        case "$word" in
            -*) continue ;;
            *) printf '%s' "$word"; return ;;
        esac
    done
    printf 'run'
}

endpoint="${HEALTHCHECK_ENDPOINT:-}"

if [ -z "$endpoint" ]; then
    mode=$(detect_mode "$(detect_argv)")
    case "$mode" in
        worker)
            # The worker's file-transfer server (which also serves /readyz and
            # /healthz) binds LOCALAI_HTTP_ADDR when set, otherwise the gRPC
            # base port minus one. See Config.resolveHTTPAddr.
            port=$(port_of "${LOCALAI_HTTP_ADDR:-}")
            if [ -z "$port" ]; then
                base=$(port_of "${LOCALAI_SERVE_ADDR:-}")
                port=$(( ${base:-50051} - 1 ))
            fi
            endpoint="http://localhost:${port}/readyz"
            ;;
        agent-worker|p2p-worker|chat|models|backends|tts|sound-generation|transcript|util|agent|mcp-server|completion)
            # Modes with no HTTP surface of their own — agent-worker and
            # p2p-worker are message-bus only, and the rest are one-shot
            # commands that exit on their own. Claiming `unhealthy` for a
            # process that was never going to bind a port is the same false
            # signal as #10987, one mode over.
            exit 0
            ;;
        *)
            # run / federated / explorer, and anything unrecognised: kong's
            # default command is `run`, so a bare `local-ai`, `local-ai --flag`
            # and `local-ai <model-name>` are all frontends on the API port.
            port=$(port_of "${LOCALAI_ADDRESS:-${ADDRESS:-}}")
            endpoint="http://localhost:${port:-8080}/readyz"
            ;;
    esac
fi

# Docker only distinguishes 0 from non-zero; normalise curl's exit codes (22 for
# the 503 a still-preloading frontend returns, 7 for connection refused) to 1 so
# the status is unambiguous in `docker inspect`.
curl -fsS -m 10 "$endpoint" >/dev/null 2>&1 || exit 1
exit 0
