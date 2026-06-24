#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

installRequirements

# No pre-baked model weights. Weights flow through LocalAI's gallery
# `files:` mechanism — see gallery entries for speechbrain-ecapa-tdnn
# and WeSpeaker / 3D-Speaker ONNX packs. SpeechBrain's
# EncoderClassifier.from_hparams also knows how to auto-download from
# HuggingFace into the configured savedir (we point it at ModelPath),
# so the first LoadModel call bootstraps the checkpoint if the gallery
# flow wasn't used.
