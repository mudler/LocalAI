#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# fish-speech uses pyrootutils which requires a .project-root marker
touch "${backend_dir}/.project-root"

installRequirements

# Clone fish-speech source (the pip package doesn't include inference modules)
FISH_SPEECH_DIR="${EDIR}/fish-speech-src"
FISH_SPEECH_REPO="https://github.com/fishaudio/fish-speech.git"
FISH_SPEECH_BRANCH="main"

if [ ! -d "${FISH_SPEECH_DIR}" ]; then
    echo "Cloning fish-speech source..."
    git clone --depth 1 --branch "${FISH_SPEECH_BRANCH}" "${FISH_SPEECH_REPO}" "${FISH_SPEECH_DIR}"
else
    echo "Updating fish-speech source..."
    cd "${FISH_SPEECH_DIR}" && git pull && cd -
fi

# Install fish-speech deps from source (without the package itself since we use PYTHONPATH)
ensureVenv
if [ "x${USE_PIP}" == "xtrue" ]; then
    pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e "${FISH_SPEECH_DIR}"
else
    uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e "${FISH_SPEECH_DIR}"
fi

# fish-speech transitive deps (wandb, tensorboard) may downgrade protobuf to 3.x
# but our generated backend_pb2.py requires protobuf 5+
ensureVenv
if [ "x${USE_PIP}" == "xtrue" ]; then
    pip install "protobuf>=5.29.0"
else
    uv pip install "protobuf>=5.29.0"
fi
