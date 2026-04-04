#!/bin/bash
# =============================================================================
# LocalAI ROCm 7.x Rebuild Script
# =============================================================================
# Builds (and optionally pushes) the main LocalAI image plus all ROCm 7.x
# backend images. No git sync — use sync-upstream.sh for merge + build + push.
#
# By default builds for ALL GPU architectures supported by ROCm 7.12.
# Override ROCM_ARCH to a subset for faster/smaller local builds, e.g.:
#   ROCM_ARCH=gfx1151 bash scripts/build-rocm.sh
#
# Usage:
#   bash scripts/build-rocm.sh               # build all + push
#   bash scripts/build-rocm.sh --no-push     # build all, no registry push
#   bash scripts/build-rocm.sh --no-backends # main image only
#   ROCM_VERSION=7.13 bash scripts/build-rocm.sh
#   ROCM_ARCH=gfx1150,gfx1151 bash scripts/build-rocm.sh
# =============================================================================
set -euo pipefail

REGISTRY="${REGISTRY:-192.168.178.127:5000}"
ROCM_VERSION="${ROCM_VERSION:-7.12}"
ROCM_ARCH="${ROCM_ARCH:-gfx803,gfx900,gfx906,gfx908,gfx90a,gfx942,gfx950,gfx1012,gfx1030,gfx1031,gfx1032,gfx1100,gfx1101,gfx1102,gfx1103,gfx1150,gfx1151,gfx1152,gfx1200,gfx1201}"
NO_PUSH=false
NO_BACKENDS=false

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'

for arg in "$@"; do
    case $arg in
        --no-push)     NO_PUSH=true ;;
        --no-backends) NO_BACKENDS=true ;;
    esac
done

# ---------------------------------------------------------------------------
# Backend list — keep in sync with sync-upstream.sh
# Format: "BACKEND_NAME|DOCKERFILE_TYPE"
# ---------------------------------------------------------------------------
BACKENDS=(
    "rerankers|python"
    "llama-cpp|llama-cpp"
    "vllm|python"
    "vllm-omni|python"
    "transformers|python"
    "diffusers|python"
    "ace-step|python"
    "kokoro|python"
    "vibevoice|python"
    "qwen-asr|python"
    "nemo|python"
    "qwen-tts|python"
    "fish-speech|python"
    "voxcpm|python"
    "pocket-tts|python"
    "faster-whisper|python"
    "whisperx|python"
    "coqui|python"
)

OUR_SUFFIX="gfx1151-rocm${ROCM_VERSION}"
BUILD_SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "local")
UPSTREAM_VERSION=$(git describe --tags --abbrev=0 upstream/master 2>/dev/null \
                    || git describe --tags --abbrev=0 2>/dev/null \
                    || echo "dev")
IMAGE_TAG="${UPSTREAM_VERSION}-${OUR_SUFFIX}-${BUILD_SHA}"
LOCAL_IMAGE="localai:${OUR_SUFFIX}"

echo -e "${BOLD}LocalAI ROCm Rebuild${NC}"
echo -e "  ROCm:    ${YELLOW}$ROCM_VERSION / $ROCM_ARCH${NC}"
echo -e "  Tag:     ${YELLOW}$IMAGE_TAG${NC}"
echo -e "  Push:    $( [ "$NO_PUSH" = "true" ] && echo "${YELLOW}disabled${NC}" || echo "${GREEN}enabled → $REGISTRY${NC}" )"
echo -e "  Backends: $( [ "$NO_BACKENDS" = "true" ] && echo "${YELLOW}skipped${NC}" || echo "${GREEN}${#BACKENDS[@]} images${NC}" )"

# ---------------------------------------------------------------------------
echo -e "\n${BOLD}=== Build main image ===${NC}"

docker build \
    --build-arg BUILD_TYPE=hipblas \
    --build-arg ROCM_VERSION=7 \
    --build-arg ROCM_ARCH="${ROCM_ARCH}" \
    --build-arg GPU_TARGETS="${ROCM_ARCH}" \
    -t "$LOCAL_IMAGE" \
    . 2>&1 | tee /tmp/localai-build-main.log

echo -e "  ${GREEN}✓ Main image built: $LOCAL_IMAGE${NC}"

# ---------------------------------------------------------------------------
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "\n${BOLD}=== Build backend images (${#BACKENDS[@]} total) ===${NC}"

    FAILED_BACKENDS=()
    for entry in "${BACKENDS[@]}"; do
        backend="${entry%%|*}"
        dftype="${entry##*|}"

        case "$dftype" in
            llama-cpp) dockerfile="backend/Dockerfile.llama-cpp"; backend_arg="" ;;
            *)         dockerfile="backend/Dockerfile.python";    backend_arg="--build-arg BACKEND=${backend}" ;;
        esac

        local_tag="localai-backends:${OUR_SUFFIX}-${backend}"
        echo -e "\n  [${backend}] Building..."

        # shellcheck disable=SC2086
        if docker build \
                --build-arg BUILD_TYPE=hipblas \
                --build-arg ROCM_VERSION=7 \
                --build-arg ROCM_ARCH="${ROCM_ARCH}" \
                $backend_arg \
                -f "$dockerfile" \
                -t "$local_tag" \
                . 2>&1 | tee "/tmp/localai-build-${backend}.log"; then
            echo -e "  ${GREEN}✓ ${backend} OK${NC}"
        else
            echo -e "  ${RED}✗ ${backend} FAILED (log: /tmp/localai-build-${backend}.log)${NC}"
            FAILED_BACKENDS+=("$backend")
        fi
    done

    if [ "${#FAILED_BACKENDS[@]}" -gt 0 ]; then
        echo -e "\n${RED}${BOLD}Failed backends:${NC} ${FAILED_BACKENDS[*]}"
        echo -e "${YELLOW}Continuing push for successfully built images.${NC}"
    else
        echo -e "\n  ${GREEN}✓ All backends built${NC}"
    fi
fi

# ---------------------------------------------------------------------------
if [ "$NO_PUSH" = "true" ]; then
    echo -e "\n${YELLOW}--no-push set — done (no registry push).${NC}"
    exit 0
fi

# ---------------------------------------------------------------------------
echo -e "\n${BOLD}=== Push main image ===${NC}"

REGISTRY_IMAGE="${REGISTRY}/localai:${IMAGE_TAG}"
REGISTRY_LATEST="${REGISTRY}/localai:latest-gfx1151"

docker tag "$LOCAL_IMAGE" "$REGISTRY_IMAGE"
docker tag "$LOCAL_IMAGE" "$REGISTRY_LATEST"

docker push "$REGISTRY_IMAGE"  && echo -e "  ${GREEN}✓ $REGISTRY_IMAGE${NC}"
docker push "$REGISTRY_LATEST" && echo -e "  ${GREEN}✓ $REGISTRY_LATEST${NC}"

# ---------------------------------------------------------------------------
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "\n${BOLD}=== Push backend images ===${NC}"

    for entry in "${BACKENDS[@]}"; do
        backend="${entry%%|*}"
        local_tag="localai-backends:${OUR_SUFFIX}-${backend}"

        if ! docker image inspect "$local_tag" &>/dev/null; then
            echo -e "  ${YELLOW}⚠ Skipping $backend (not built)${NC}"
            continue
        fi

        reg_versioned="${REGISTRY}/localai-backends:${IMAGE_TAG}-${backend}"
        reg_latest="${REGISTRY}/localai-backends:latest-gfx1151-${backend}"

        docker tag "$local_tag" "$reg_versioned"
        docker tag "$local_tag" "$reg_latest"
        docker push "$reg_versioned" && echo -e "  ${GREEN}✓ $reg_versioned${NC}"
        docker push "$reg_latest"    && echo -e "  ${GREEN}✓ $reg_latest${NC}"
    done
fi

# ---------------------------------------------------------------------------
echo -e "\n${GREEN}${BOLD}=== Done ===${NC}"
echo -e "  Main image: ${GREEN}$REGISTRY_IMAGE${NC}"
echo -e "  Latest:     ${GREEN}$REGISTRY_LATEST${NC}"
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "  Backends:   ${GREEN}${#BACKENDS[@]} images tagged as latest-gfx1151-<name>${NC}"
fi
echo ""
echo -e "  Deploy:"
echo -e "  ${YELLOW}docker compose -f docker-compose-gfx1151.yaml up -d localai --force-recreate${NC}"
