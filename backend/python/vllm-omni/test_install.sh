#!/bin/bash
# SPDX-License-Identifier: MIT
# Exercise the real installer without downloading packages or requiring a GPU.
set -eu
backend_dir=$(cd "$(dirname "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
cp "$backend_dir/install.sh" "$test_dir/"
mkdir -p "$test_dir/common" "$test_dir/bin"
cat > "$test_dir/common/libbackend.sh" <<'EOF'
installRequirements() { echo base >> "$INSTALL_LOG"; }
runProtogen() { echo protogen >> "$INSTALL_LOG"; }
EOF
cat > "$test_dir/bin/mock" <<'EOF'
#!/bin/bash
printf '%s %s\n' "${0##*/}" "$*" >> "$INSTALL_LOG"
if [[ "$*" == *"install . "* ]]; then
    # v0.14's package-data lists only generic stage configs. A regular wheel
    # needs an explicit manifest rule to retain ROCm/NPU/XPU YAML resources.
    if ! grep -qx 'recursive-include vllm_omni \*.yaml' MANIFEST.in; then
        echo 'FAIL: platform stage configs are not included in the wheel' >&2
        exit 23
    fi
    if [ "$TEST_ARCH" = aarch64 ]; then
        ! grep -q fa3-fwd requirements/cuda.txt || exit 20
        ! grep -q fa3-fwd pyproject.toml || exit 21
        grep -q other-dependency requirements/cuda.txt || exit 22
    fi
    printf '%s\n' "$PWD" > "$INSTALL_LOG.source"
fi
case ${0##*/} in
uname) echo "$TEST_ARCH" ;;
python)
    # The harness checks installer ordering; real builds execute the metadata
    # validation against the installed wheel using the backend interpreter.
    [ -f "$INSTALL_LOG.source" ] || exit 24
    # Source egg-info must not shadow the installed distribution metadata.
    [ "$*" = '-I -' ] || exit 25
    cat > "$INSTALL_LOG.validation"
    ;;
git)
    if [ "$1" = clone ]; then mkdir -p vllm-omni; fi
    mkdir -p requirements
    printf 'fa3-fwd==0.0.3\nother-dependency\n' > requirements/cuda.txt
    printf '"fa3-fwd==0.0.1",\n' > pyproject.toml
    ;;
esac
EOF
chmod +x "$test_dir/bin/mock"
for command in pip uv git uname python; do ln -s mock "$test_dir/bin/$command"; done
export PATH="$test_dir/bin:$PATH" INSTALL_LOG="$test_dir/install.log"
fail() { echo "FAIL: $*" >&2; cat "$INSTALL_LOG" >&2; exit 1; }
cd "$test_dir"
for profile in cublas12 hipblas cublas13 l4t13; do
    export BUILD_PROFILE=$profile BUILD_TYPE=cublas TEST_ARCH=x86_64
    version=0.14.0
    revision=ed89c8b0436999e9210f11363f4eb512330a9dfa
    case $profile in
    hipblas) BUILD_TYPE=hipblas ;;
    cublas13|l4t13) version=0.20.0; revision=4a24a517abc7769b1399ded594558a3fe8269872 ;;
    esac
    if [ "$profile" = l4t13 ]; then TEST_ARCH=aarch64; fi
    for USE_PIP in false true; do
        export USE_PIP
        : > "$INSTALL_LOG"
        rm -f "$INSTALL_LOG.source" "$INSTALL_LOG.validation"
        bash install.sh
        [ -s "$INSTALL_LOG.validation" ] || fail "$profile: installed resources were not checked"
        grep -q "$revision" "$INSTALL_LOG" || fail "$profile: source is not pinned"
        grep -q "vllm==$version" "$INSTALL_LOG" || fail "$profile: engine is not pinned"
        if grep -q ' -e ' "$INSTALL_LOG"; then fail "$profile: editable package cannot relocate"; fi
        if [ "$USE_PIP" = true ] && grep -q -- '--torch-backend' "$INSTALL_LOG"; then
            fail "$profile: pip does not accept --torch-backend"
        fi
        last_install=$(grep ' install ' "$INSTALL_LOG" | tail -1)
        case $last_install in *"vllm==$version"*) ;; *) fail "$profile: Omni resolution must retain engine pin" ;; esac
        [ "$(tail -1 "$INSTALL_LOG")" = protogen ] || fail 'protobuf must regenerate after installation'
        [ ! -e "$(cat "$INSTALL_LOG.source")" ] || fail "temporary source checkout was not removed"
        grep -q "git tag v$version" "$INSTALL_LOG" || fail "release metadata is missing"
        echo "PASS: $profile USE_PIP=$USE_PIP"
    done
done
