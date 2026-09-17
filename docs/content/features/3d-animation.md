+++
title = "3D Animation"
weight = 20
url = "/features/3d-animation/"
+++

LocalAI supports text-to-motion with **kimodo.cpp** on CPU and Vulkan. In
**Studio → 3D**, selecting an animation model replaces the image input with a
motion prompt and the selected model's animation controls.

The Studio preview plays at real-time speed using the GLB's timestamps. Slow or
delayed display frames skip ahead rather than slowing the animation. Pause and
the timeline let you inspect individual poses; playback resumes from that point.

Kimodo exports an animated skeleton GLB: joint rotations and root movement,
without a mesh or skin. SOMA models produce the compact 30-joint skeleton;
G1 models produce 34 joints. These outputs are animation assets, not textured
characters, and cannot use TRELLIS print remeshing.

## Setup and runtime options

Install `kimodo-soma-rp`, `kimodo-soma-seed`, `kimodo-g1-rp`, or
`kimodo-g1-seed` from the model gallery. For example:

```bash
local-ai run kimodo-soma-rp
```

Importing the corresponding `LocalAI-io/Kimodo-*-GGML` Hugging Face repository
also installs the motion weights and the shared Llama-3-Kimodo text bundle.
These are multi-file models, not standalone language-model GGUFs. The shared
text bundle is stored once under `kimodo/text`. SMPL-X weights are not included
in the gallery because their redistribution terms differ.

Linux has CPU and Vulkan builds on amd64 and arm64. Apple Silicon uses the
CPU build; this upstream engine does not implement Metal. Linux x86 CPU
packages select AVX2/FMA/F16C/BMI2 where supported, with a portable fallback.
Containers must expose the host GPU's Vulkan ICD and driver libraries, not just
CUDA compute devices. On NVIDIA, enable graphics driver capabilities alongside
compute; on NixOS CDI installations, check that the NVIDIA Vulkan manifest is
also mounted inside the container.

An installed model can override the defaults in its YAML configuration:

```yaml
backend: kimodocpp
parameters:
  model: kimodo/kimodo-soma-rp-v1.1-f32.gguf
threads: 8
options:
  - text_bundle:kimodo/text
  - device:auto
  - text_layer_chunk:8
  - frames:150
  - steps:100
  - text_guidance:2
```

`device` accepts `auto`, `cpu`, or `vulkan`; explicitly requesting unavailable
Vulkan fails rather than silently using CPU. `threads` applies to both the text
encoder and motion model. `text_layer_chunk` (1–32, default 8) trades text-encoder
working memory against loading overhead. The backend keeps one motion-model
session loaded and serializes generation; it does not launch a CLI or reload
motion weights for every request. Text-encoder layers are streamed in chunks.

## API

`POST /3d/animate` accepts named, typed conditioning inputs and model-specific
parameters. Query `/v1/models/capabilities` and inspect `three_d_operations`
for the selected model's supported inputs, controls, and defaults. Animation
does not universally require text: other backends may declare video or a
combination of mesh and text inputs.

```json
{
  "model": "kimodo-soma-rp",
  "inputs": {
    "prompt": {"type": "text", "data": "A person walks forward and waves."}
  },
  "params": {"frames": "150", "steps": "100", "text_guidance": "2", "seed": "42"},
  "response_format": "url"
}
```

The response contains `data[0].url` for the generated `.glb`, or
`data[0].b64_json` when `response_format` is `b64_json`. The endpoint uses the
existing **3D Generation** permission and the selected model's access controls.
`/3d/generations` and `/3d/remesh` retain their existing mesh workflows.

Kimodo accepts one prompt with 60–150 frames at 30 FPS. The reference defaults
are 150 frames, 100 sampling steps, and text guidance 2. Parameters are strings;
unsupported inputs and parameters are rejected. Multi-prompt transitions and
mesh retargeting are not currently exposed by this adapter.
