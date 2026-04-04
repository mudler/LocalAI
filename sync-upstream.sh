#!/bin/bash
# =============================================================================
# LocalAI Upstream Sync — ROCm 7.x Fork
# =============================================================================
# Usage:
#   bash sync-upstream.sh               # fetch + merge + build all + push
#   bash sync-upstream.sh --dry-run     # fetch + merge only (no build)
#   bash sync-upstream.sh --no-push     # build but no registry push
#   bash sync-upstream.sh --no-backends # main image only, skip backend images
#   ROCM_VERSION=7.13 bash sync-upstream.sh
#
# Image tags use rocm<ROCM_VERSION> (e.g. rocm7.12), not a GPU-specific name,
# because ROCm 7.x is a distinct build/install paradigm from ROCm 6.x and the
# resulting images support all rocWMMA-capable architectures.
#
# Image tags (main image):
#   <REGISTRY>/localai:<VERSION>-rocm<ROCM_VERSION>-<GIT_SHA>  (build-specific)
#   <REGISTRY>/localai:<VERSION>-rocm<ROCM_VERSION>            (rolling per version)
#   <REGISTRY>/localai:latest-rocm<ROCM_MAJOR>                  (rolling latest, e.g. latest-rocm7)
#
# Image tags (each backend):
#   <REGISTRY>/localai-backends:<VERSION>-rocm<ROCM_VERSION>-<GIT_SHA>-<BACKEND>
#   <REGISTRY>/localai-backends:latest-rocm<ROCM_MAJOR>-<BACKEND>
#
# ROCm version changes only when a new ROCm release ships — override via env var.
# On conflict: script stops, resolve manually, git commit, then re-run.
# =============================================================================
set -euo pipefail

REGISTRY="${REGISTRY:-192.168.178.127:5000}"
ROCM_VERSION="${ROCM_VERSION:-7.12}"
ROCM_ARCH="${ROCM_ARCH:-gfx803,gfx900,gfx906,gfx908,gfx90a,gfx942,gfx950,gfx1012,gfx1030,gfx1031,gfx1032,gfx1100,gfx1101,gfx1102,gfx1103,gfx1150,gfx1151,gfx1152,gfx1200,gfx1201}"
UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"
UPSTREAM_BRANCH="${UPSTREAM_BRANCH:-master}"
DRY_RUN=false
NO_PUSH=false
NO_BACKENDS=false

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'

for arg in "$@"; do
    case $arg in
        --dry-run)     DRY_RUN=true ;;
        --no-push)     NO_PUSH=true ;;
        --no-backends) NO_BACKENDS=true ;;
    esac
done

# ---------------------------------------------------------------------------
# Backend list — all ROCm 7.x backend images to build.
# Format: "BACKEND_NAME|DOCKERFILE_TYPE"
#   python    → backend/Dockerfile.python  (passes --build-arg BACKEND=<name>)
#   llama-cpp → backend/Dockerfile.llama-cpp
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

OUR_SUFFIX="rocm${ROCM_VERSION}"

# ---------------------------------------------------------------------------
echo -e "${BOLD}=== 1. Upstream fetch ===${NC}"
git fetch "$UPSTREAM_REMOTE"

UPSTREAM_VERSION=$(git describe --tags --abbrev=0 "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" 2>/dev/null || echo "dev")
echo -e "  Upstream: ${YELLOW}$UPSTREAM_REMOTE/$UPSTREAM_BRANCH${NC} @ ${GREEN}$UPSTREAM_VERSION${NC}"
echo -e "  ROCm:     ${YELLOW}$ROCM_VERSION${NC} / arch ${YELLOW}$ROCM_ARCH${NC}"

BEHIND=$(git rev-list HEAD.."$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" --count)
if [ "$BEHIND" = "0" ]; then
    echo -e "  ${GREEN}✓ Already up to date — no merge needed${NC}"
    if [ "$DRY_RUN" = "true" ]; then exit 0; fi
else
    echo -e "  ${YELLOW}⚠ $BEHIND new upstream commits${NC}"

    # -------------------------------------------------------------------------
    echo -e "\n${BOLD}=== 2. Merge upstream/$UPSTREAM_BRANCH ===${NC}"
    if ! git merge "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" --no-edit \
            -m "chore: merge upstream $UPSTREAM_VERSION into rocm7 fork"; then
        echo -e "\n${RED}✗ Merge conflicts! Manual resolution required:${NC}"
        echo ""
        git diff --name-only --diff-filter=U
        echo ""
        echo -e "  Steps:"
        echo -e "  1. Fix conflicts in the files listed above"
        echo -e "  2. ${YELLOW}git add <file>${NC} for each resolved file"
        echo -e "  3. ${YELLOW}git commit${NC} to complete the merge"
        echo -e "  4. Re-run this script"
        echo ""
        echo -e "  Or abort: ${YELLOW}git merge --abort${NC}"
        exit 1
    fi
    echo -e "  ${GREEN}✓ Merge successful${NC}"
fi

if [ "$DRY_RUN" = "true" ]; then
    echo -e "\n${YELLOW}Dry-run — no build.${NC}"
    exit 0
fi

# ---------------------------------------------------------------------------
echo -e "\n${BOLD}=== 3. Pre-build checks ===${NC}"
FAIL=0

if grep -q "^ENV GGML_CUDA_ENABLE_UNIFIED_MEMORY" Dockerfile 2>/dev/null; then
    echo -e "  ${RED}✗ GGML_CUDA_ENABLE_UNIFIED_MEMORY in Dockerfile! Remove it.${NC}"
    FAIL=1
else
    echo -e "  ${GREEN}✓ GGML_CUDA_ENABLE_UNIFIED_MEMORY not in Dockerfile${NC}"
fi

if grep -qE "core-7\.[0-9]" backend/Dockerfile.llama-cpp backend/Dockerfile.python 2>/dev/null; then
    echo -e "  ${RED}✗ Hardcoded core-7.XX in backend Dockerfiles — use core-7 (update-alternatives).${NC}"
    grep -n "core-7\.[0-9]" backend/Dockerfile.llama-cpp backend/Dockerfile.python 2>/dev/null || true
    FAIL=1
else
    echo -e "  ${GREEN}✓ No hardcoded core-7.XX in backend Dockerfiles${NC}"
fi

[ "$FAIL" = "1" ] && { echo -e "\n${RED}Checks failed. Build aborted.${NC}"; exit 1; }

# ---------------------------------------------------------------------------
BUILD_SHA=$(git rev-parse --short HEAD)
VERSION_TAG="${UPSTREAM_VERSION}-${OUR_SUFFIX}"
IMAGE_TAG="${VERSION_TAG}-${BUILD_SHA}"
LOCAL_IMAGE="localai:${OUR_SUFFIX}"

echo -e "\n${BOLD}=== 4. Build main image ===${NC}"
echo -e "  Build tag:   ${YELLOW}$IMAGE_TAG${NC}"
echo -e "  Version tag: ${YELLOW}$VERSION_TAG${NC}"
echo -e "  Latest tag:  ${YELLOW}latest-rocm${ROCM_VERSION%%.*}${NC}"

docker build \
    --build-arg BUILD_TYPE=hipblas \
    --build-arg ROCM_VERSION=7 \
    --build-arg ROCM_ARCH="${ROCM_ARCH}" \
    --build-arg GPU_TARGETS="${ROCM_ARCH}" \
    -t "$LOCAL_IMAGE" \
    . 2>&1 | tee /tmp/localai-build-main.log

echo -e "  ${GREEN}✓ Main image built${NC}"

# ---------------------------------------------------------------------------
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "\n${BOLD}=== 5. Build backend images (${#BACKENDS[@]} total) ===${NC}"

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
        echo -e "\n  ${GREEN}✓ All backends built successfully${NC}"
    fi
fi

# ---------------------------------------------------------------------------
if [ "$NO_PUSH" = "true" ]; then
    echo -e "\n${YELLOW}--no-push set — skipping registry push.${NC}"
    echo -e "  Local main image: ${GREEN}$LOCAL_IMAGE${NC}"
    exit 0
fi

# ---------------------------------------------------------------------------
echo -e "\n${BOLD}=== 6. Push main image ===${NC}"

ROCM_MAJOR="${ROCM_VERSION%%.*}"   # e.g. "7" from "7.12"
REGISTRY_IMAGE="${REGISTRY}/localai:${IMAGE_TAG}"
REGISTRY_VERSION="${REGISTRY}/localai:${VERSION_TAG}"
REGISTRY_LATEST="${REGISTRY}/localai:latest-rocm${ROCM_MAJOR}"

docker tag "$LOCAL_IMAGE" "$REGISTRY_IMAGE"
docker tag "$LOCAL_IMAGE" "$REGISTRY_VERSION"
docker tag "$LOCAL_IMAGE" "$REGISTRY_LATEST"

docker push "$REGISTRY_IMAGE"   && echo -e "  ${GREEN}✓ $REGISTRY_IMAGE${NC}"
docker push "$REGISTRY_VERSION" && echo -e "  ${GREEN}✓ $REGISTRY_VERSION${NC}"
docker push "$REGISTRY_LATEST"  && echo -e "  ${GREEN}✓ $REGISTRY_LATEST${NC}"

# ---------------------------------------------------------------------------
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "\n${BOLD}=== 7. Push backend images ===${NC}"

    for entry in "${BACKENDS[@]}"; do
        backend="${entry%%|*}"
        local_tag="localai-backends:${OUR_SUFFIX}-${backend}"

        # Skip backends that failed to build
        if ! docker image inspect "$local_tag" &>/dev/null; then
            echo -e "  ${YELLOW}⚠ Skipping $backend (not built)${NC}"
            continue
        fi

        reg_versioned="${REGISTRY}/localai-backends:${IMAGE_TAG}-${backend}"
        reg_latest="${REGISTRY}/localai-backends:latest-rocm${ROCM_MAJOR}-${backend}"

        docker tag "$local_tag" "$reg_versioned"
        docker tag "$local_tag" "$reg_latest"
        docker push "$reg_versioned" && echo -e "  ${GREEN}✓ $reg_versioned${NC}"
        docker push "$reg_latest"    && echo -e "  ${GREEN}✓ $reg_latest${NC}"
    done
fi

# ---------------------------------------------------------------------------
echo -e "\n${BOLD}=== 8. Git tag ===${NC}"
GIT_TAG="${IMAGE_TAG}"
if git tag -l | grep -q "^${GIT_TAG}$"; then
    echo -e "  ${YELLOW}Tag $GIT_TAG already exists — skipped${NC}"
else
    git tag "$GIT_TAG"
    git push origin "$GIT_TAG" 2>/dev/null || echo -e "  ${YELLOW}(Tag push failed — set locally)${NC}"
    echo -e "  ${GREEN}✓ Tag: $GIT_TAG${NC}"
fi

# ---------------------------------------------------------------------------
echo -e "\n${GREEN}${BOLD}=== Done ===${NC}"
echo -e "  Upstream:   ${GREEN}$UPSTREAM_VERSION${NC}"
echo -e "  ROCm:       ${GREEN}$ROCM_VERSION / $ROCM_ARCH${NC}"
echo -e "  Main image: ${GREEN}$REGISTRY_IMAGE${NC}"
echo -e "  Latest:     ${GREEN}$REGISTRY_LATEST${NC}"
if [ "$NO_BACKENDS" = "false" ]; then
    echo -e "  Backends:   ${GREEN}${#BACKENDS[@]} images tagged as latest-rocm${ROCM_MAJOR}-<name>${NC}"
fi
echo ""
echo -e "  Deploy:"
echo -e "  ${YELLOW}docker compose pull localai && docker compose up -d localai --force-recreate${NC}"
echo ""
echo -e "  Next run:"
echo -e "  ${YELLOW}bash sync-upstream.sh${NC}"
echo -e "  ${YELLOW}ROCM_VERSION=7.13 bash sync-upstream.sh${NC}  (when new ROCm version ships)"
