#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

installRequirements

# Pre-bake the default model bundles so first-run is offline-clean.
#
# IMPORTANT: the final image is built FROM scratch with
# `COPY --from=builder /${BACKEND}/ /`, so only paths under the backend
# directory survive. We must stage models inside this directory rather
# than /root/.insightface or /opt/.
MODELS_ROOT="${backend_dir}/models/insightface"
OPENCV_DIR="${backend_dir}/models/opencv"
install -d "${MODELS_ROOT}/models"
install -d "${OPENCV_DIR}"

# 1. buffalo_l (insightface default; NON-COMMERCIAL research use only).
#    FaceAnalysis auto-downloads to <root>/models/<name>/; we override
#    the root so the download lands under ${backend_dir}.
python -c "from insightface.app import FaceAnalysis; \
           FaceAnalysis(name='buffalo_l', root='${MODELS_ROOT}', providers=['CPUExecutionProvider']).prepare(ctx_id=-1)"

# 2. OpenCV Zoo (Apache 2.0 — commercial-safe).
#    These are not on pypi: fetch the ONNX files directly so the
#    OnnxDirectEngine can load them without network access at runtime.
curl -fsSL -o "${OPENCV_DIR}/yunet.onnx" \
    https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx
curl -fsSL -o "${OPENCV_DIR}/sface.onnx" \
    https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx
