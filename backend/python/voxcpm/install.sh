#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

installRequirements

# Apply patch to fix PyTorch compatibility issue in voxcpm
# This fixes the "Dimension out of range" error in scaled_dot_product_attention
# by changing .contiguous() to .unsqueeze(0) in the attention module
# The patch is needed because voxcpm's initialization test generation fails with
# certain PyTorch versions due to a bug in scaled_dot_product_attention
# https://github.com/OpenBMB/VoxCPM/issues/71#issuecomment-3441789452
VOXCPM_PATH=$(python -c "import voxcpm; import os; print(os.path.dirname(voxcpm.__file__))" 2>/dev/null || echo "")
if [ -n "$VOXCPM_PATH" ] && [ -f "$VOXCPM_PATH/modules/minicpm4/model.py" ]; then
    echo "Applying patch to voxcpm at $VOXCPM_PATH/modules/minicpm4/model.py"
    # Replace .contiguous() with .unsqueeze(0) for the three lines in the attention forward_step method
    # This fixes the dimension error in scaled_dot_product_attention
    sed -i 's/query_states = query_states\.contiguous()/query_states = query_states.unsqueeze(0)/g' "$VOXCPM_PATH/modules/minicpm4/model.py"
    sed -i 's/key_cache = key_cache\.contiguous()/key_cache = key_cache.unsqueeze(0)/g' "$VOXCPM_PATH/modules/minicpm4/model.py"
    sed -i 's/value_cache = value_cache\.contiguous()/value_cache = value_cache.unsqueeze(0)/g' "$VOXCPM_PATH/modules/minicpm4/model.py"
    echo "Patch applied successfully"
else
    echo "Warning: Could not find voxcpm installation to apply patch (path: ${VOXCPM_PATH:-not found})"
fi
