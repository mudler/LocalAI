#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

installRequirements

# We deliberately do NOT pre-bake any model weights here. Two reasons:
#
#   1. Weights should follow LocalAI's gallery-managed download flow
#      like every other backend. For OpenCV Zoo (YuNet + SFace) the
#      gallery entries in gallery/index.yaml list the ONNX files via
#      `files:` with URI + SHA-256 — LocalAI fetches them into the
#      models directory on `local-ai models install`.
#
#   2. For insightface model packs (buffalo_l, buffalo_s, buffalo_m,
#      buffalo_sc, antelopev2), upstream distributes zip archives
#      only (no individual ONNX URLs). We rely on insightface's own
#      auto-download machinery (`FaceAnalysis(name=<pack>, root=<dir>)`)
#      at first LoadModel, pointed at a writable directory. This
#      matches how rfdetr behaves (uses `inference.get_model()`).
#
# Net effect: the backend image ships only Python deps (~150MB CPU).
