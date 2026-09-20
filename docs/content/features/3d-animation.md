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
Each installation needs three files: the motion GGUF, a monolithic text-encoder
GGUF, and `tokenizer.gguf`. Text weights and the tokenizer are shared under
`kimodo/text`; SOMA and G1 do not download separate copies of the same encoder.
SMPL-X weights are not included
in the gallery because their redistribution terms differ.

The default entries use **Q8_0**, matching upstream's performance default.
Other text-encoder quantizations are available through the `-q6_k`, `-q5_k`,
`-q4_k_m`, `-q4_k`, and `-bf16` gallery entries, for example
`kimodo-g1-rp-q4_k_m`. Quantization affects the text encoder, not the F32 motion
weights. Lower-bit encoders reduce memory use but can change the generated motion.
These are explicit choices, not automatically ranked alternatives to Q8_0.

The model importer also accepts a `text_quantization` preference with `q8_0`
(default), `q6_k`, `q5_k`, `q4_k_m`, `q4_k`, or `bf16`. To change an existing
installation manually, download the selected encoder beside `tokenizer.gguf`
and update `text_bundle` to its GGUF path.

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
  - text_bundle:kimodo/text/Llama-3-Kimodo-Q8_0.gguf
  - device:auto
  - text_layer_chunk:32
  - frames:150
  - steps:100
  - text_guidance:2
```

`device` accepts `auto`, `cpu`, or `vulkan`; explicitly requesting unavailable
Vulkan fails rather than silently using CPU. `threads` applies to both the text
encoder and motion model. `text_layer_chunk` accepts **1–32 and defaults to 32**:
all text-encoder layers stay resident across requests for maximum throughput.
Set it to `8` (or another smaller value) to load fewer layers at a time when
memory is limited. Full residency still executes bounded eight-layer graphs;
resident weight count and execution graph size are separate concerns.

The backend keeps one native session loaded and serializes generation. It
reuses text and motion weights, packed attention/LoRA paths, and upstream's
cached motion execution graphs instead of reloading and rebuilding per request.
The graph cache is bounded to the most recent batch/frame shape. Cold loading
therefore takes longer than subsequent generations.

Allow memory for motion weights and execution buffers as well as the encoder.
BF16 text weights alone are about 14.1 GiB and should use a smaller
`text_layer_chunk` on a 16 GiB GPU. Each loaded motion model has its own native
session and resident memory, even when model files are shared on disk.
Upstream's optional `KIMODO_TEXT_RESIDENT_LIMIT_MIB` environment variable can
also impose a residency ceiling; above it, a request for 32 layers uses streaming.

Existing `text_bundle` directory paths remain supported for legacy layer-file
installations. They also adopt the new 32-layer default unless explicitly
configured otherwise. Switching to monolithic weights does not delete the old
files; remove them only after confirming no model configuration still uses them.

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

## Usage accounting

Kimodo reports one set of usage measurements through **generic backend metadata**.
The gRPC `Result` has a `metadata` bytes field containing a JSON object; there is
no separate gRPC usage field or model-specific usage message.

### Backend metadata

The JSON stored in `Result.metadata` looks like this:

```json
{
  "usage": {
    "input_units": 32,
    "output_units": 15000,
    "accounting_rule": "frame_steps_v1",
    "details": {
      "output_frames": 150,
      "sampling_steps": 100
    }
  }
}
```

`usage` is an application convention **inside** the generic metadata object.
Other metadata keys and model-specific `details` can be added without changing
the gRPC definitions or database schema.

For kimodo, input units are the tokens produced by its text encoder's tokenizer,
including the beginning-of-sequence (BOS) token. Output units are actual generated
frames multiplied by effective sampling steps, after applying model defaults and
request overrides. In the example, 150 frames × 100 steps = 15,000 output units.

### HTTP response and recorded usage

For `POST /3d/animate`, LocalAI returns the backend object under `metadata`.
Usage appears only at `metadata.usage`; there is no top-level `usage` summary.
The following is an excerpt from either a `url` or `b64_json` response:

```json
{
  "metadata": {
    "usage": {
      "input_units": 32,
      "output_units": 15000,
      "accounting_rule": "frame_steps_v1",
      "details": {
        "output_frames": 150,
        "sampling_steps": 100
      }
    }
  }
}
```

Internal accounting reads `metadata.usage` directly and records each successful
request once. The existing token-named database columns store the unit counts:

| Response metadata | Usage record column |
|---|---|
| `metadata.usage.input_units` | `PromptTokens` |
| `metadata.usage.output_units` | `CompletionTokens` |
| Sum of input and output units | `TotalTokens` |

The record's single `Metadata` column retains the complete backend JSON,
including the rule and frame/step breakdown. With statistics disabled, response
metadata is still returned but no usage record is created.

Both unit counts must be present, nonnegative integers whose sum fits a Go `int`.
Explicit zero counts are valid. If metadata is absent, the response omits
`metadata`. Metadata without a `usage` member is returned, but creates no usage
record. Malformed metadata or invalid usage counts cause metadata to be omitted
and a warning to be logged; the generated asset is still returned. Failed
generations produce no usage record. Counts are never estimated for backends
that do not report them.

### Pricing units

Kimodo's output units are **frame-steps**, not text tokens. They approximate
computational work rather than runtime or hardware cost. A price per million
output tokens therefore means dollars per million frame-steps for this model:

`cost = (input_units × input_price + output_units × output_price) / 1,000,000`

This describes how to interpret the existing token-based pricing dimensions;
it does not set a price or charge money automatically.
