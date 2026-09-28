+++
disableToc = false
title = "3D Generation"
weight = 19
url = "/features/3d-generation/"
+++

LocalAI supports single-image TRELLIS.2 and four-view Pixal3D generation. The TRELLIS.2 workflow below generates textured 3D meshes from a single conditioning image via the `/3d/generations` endpoint, powered by the `trellis2cpp` backend — a C++/GGML port of [Microsoft TRELLIS.2](https://github.com/microsoft/TRELLIS.2) ([trellis2.cpp](https://github.com/localai-org/trellis2cpp)). The output is a binary glTF (`.glb`) asset with PBR materials.

Generation is image-conditioned only — there is no text-prompt path. Provide a photo or rendering of a single object (ideally on a plain background) and TRELLIS.2 reconstructs a full 3D mesh from it.

For text-conditioned animated skeletons, see [3D Animation](/features/3d-animation/). Both workflows appear under **3D** in Studio; selecting a model changes the inputs and generation options.

## Setup

Install a model from the gallery:

```bash
local-ai run trellis2-4b          # full pipeline: 1024³ cascade + PBR textures (~18 GB)
# or
local-ai run trellis2-4b-geometry # 512³ untextured geometry only (~7 GB)
```

The backend detects which component GGUFs are present and degrades gracefully: without the texture models it produces untextured geometry, and without the fine-flow models it falls back to a coarse marching-cubes preview.

## API

- **Method:** `POST`
- **Endpoint:** `/3d/generations`

### Request

The request body is JSON with the following fields:

| Parameter         | Type     | Required | Default | Description                                                        |
|-------------------|----------|----------|---------|--------------------------------------------------------------------|
| `model`           | `string` | Yes      |         | Model name to use                                                  |
| `image`           | `string` | Yes      |         | Conditioning image as base64, a data URI, or a public URL          |
| `quality`         | `string` | No       | `auto`  | Mesh pipeline: `auto`, `coarse`, `512`, or `1024`                  |
| `background`      | `string` | No       | `auto`  | Background handling: `auto`, `keep`, `black`, or `white`           |
| `step`            | `int`    | No       | 12      | Flow sampling steps for the shape                                  |
| `texture_steps`   | `int`    | No       | 12      | Flow sampling steps for the PBR material                           |
| `cfg_scale`       | `float`  | No       | 7.5     | Classifier-free guidance scale                                     |
| `seed`            | `int`    | No       | random  | Random seed for reproducibility                                    |
| `response_format` | `string` | No       | `url`   | `url` to return a file URL, `b64_json` for base64 output           |
| `params`          | `object` | No       |         | Backend-specific string parameters (`texture_size`, `components`)  |

`quality` selects the mesh resolution: `coarse` is a fast marching-cubes preview, `512` the fine dual-grid mesh, `1024` the high-resolution cascade (slow — several minutes, roughly 10 GB VRAM), and `auto` picks the best pipeline the installed model set supports.

`background` controls solid-background removal on the conditioning image before generation: `auto` detects border-connected near-black/near-white, `keep` preserves the image alpha exactly, and `black`/`white` force removal of that colour.

Backend-specific `params`: `texture_size` (UV-atlas resolution hint when atlas baking is enabled) and `components` (`tiny` removes small islands, `largest` keeps only the biggest connected component, `all` — the default — keeps everything).

### Response

Returns a JSON response using LocalAI's OpenAI-style generation envelope:

| Field             | Type     | Description                                                    |
|-------------------|----------|----------------------------------------------------------------|
| `created`         | `int`    | Unix timestamp of generation                                   |
| `id`              | `string` | Unique identifier (UUID)                                       |
| `data`            | `array`  | Array with the generated asset                                 |
| `data[].url`      | `string` | URL path to the `.glb` under `/generated-3d` (if `url`)        |
| `data[].b64_json` | `string` | Base64-encoded GLB (if `response_format` is `b64_json`)        |

### Watertight print remeshing

`POST /3d/remesh` applies the same post-generation CGAL Alpha Wrap workflow as the trellis2.cpp demo. It accepts `multipart/form-data` and returns the remeshed GLB directly as `model/gltf-binary`:

| Field    | Type     | Required | Default | Description |
|----------|----------|----------|---------|-------------|
| `model`  | `string` | Yes      |         | Installed TRELLIS.2 model name |
| `mesh`   | `file`   | Yes      |         | Source GLB produced by TRELLIS.2 |
| `detail` | `float`  | No       | `0.5`   | Smallest preserved detail as a percentage of the source bounding-box diagonal (`0.35`–`2.5`) |

There is intentionally no independent offset control. The enclosing offset follows the trellis2.cpp demo and is derived as `detail / 30`; independent tuning tends to produce puffy or degenerate wraps. Lower detail percentages retain finer features but take longer and generally produce more triangles. The output is watertight, oriented, intersection-free, and 2-manifold. For textured sources, LocalAI unwraps the replacement mesh and reprojects its PBR material onto a new UV atlas.

Source GLBs may be up to 512 MiB. This route uses its own upload limit because fine TRELLIS.2 meshes commonly exceed LocalAI's default `--upload-limit`.

```bash
curl http://localhost:8080/3d/remesh \
  -F model=trellis2-4b \
  -F mesh=@generated.glb \
  -F detail=0.5 \
  --output printable.glb
```

## Usage

### Generate a 3D model from an image

```bash
curl http://localhost:8080/3d/generations \
  -H "Content-Type: application/json" \
  -d '{
    "model": "trellis2-4b",
    "image": "https://example.com/photo-of-a-chair.png",
    "quality": "512"
  }'
```

The response contains a URL such as `/generated-3d/b64123456789.glb`; fetch it from the same server. The GLB is standard glTF 2.0 and opens in Blender, three.js, `<model-viewer>`, and most engines.

### Base64 input and output

```bash
curl http://localhost:8080/3d/generations \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"trellis2-4b\",
    \"image\": \"$(base64 -w0 chair.png)\",
    \"response_format\": \"b64_json\"
  }" | jq -r '.data[0].b64_json' | base64 -d > chair.glb
```

## WebUI

The React UI includes a 3D tab in the Studio (and a `/3d` page) with an interactive PBR viewer: upload or paste an image from the clipboard, pick the quality, and preview the generated mesh with orbit/pan/zoom and a wireframe toggle. Past generations are kept in the browser (IndexedDB). After generation, a single Detail slider and **Apply remeshing** button replace the preview with the exact watertight model that the GLB download exports; **Show original** switches back without regenerating.

## Notes

- The 512³ pipeline takes roughly two minutes on a modern GPU; the 1024³ cascade takes around five minutes and needs about 10 GB VRAM plus a temporary host-RAM spike.
- `TRELLIS2_DEVICE=cpu` forces CPU inference (slow; mainly for debugging).
- The generated mesh has unoriented winding (faithful to TRELLIS.2) and is exported Y-up with vertex-PBR materials; a UV-atlas texture bake can be enabled in the backend via the `T2GLB_XATLAS` environment variable.

## Pixal3D: four views to a mesh

The `pixal3dcpp` backend runs [raven38/pixal3d.cpp](https://github.com/raven38/pixal3d.cpp) in its explicit Pixal3D multiview mode. It produces a textured GLB with PNG textures at resolution 1024.

Use four **8-bit RGBA PNGs**, ordered **front, right, back, left**. Prepare the alpha mattes before uploading. Each view can contain at most 32 MiB and 4096 × 4096 pixels. URLs, base64 strings, and data URIs use the same input staging as single-image generation; client-local file paths are not accepted.

The server upload limit also applies to the complete JSON request. Set `--upload-limit` to accommodate all four images and their base64 overhead.

These are canonical turntable views, not arbitrary camera photographs. Match the upstream canonical rig: 20° field of view, zero elevation, and camera distance 3.1192049980163574. Set `mesh_scale` to the finite positive object scale used for those views. The backend does not estimate cameras, create alpha mattes, or accept `transforms.json` uploads.

### Install the model

In **Import model**, select `pixal3dcpp` explicitly and enter:

```text
https://huggingface.co/raven38/pixal3d-q8_0-v1
```

The importer downloads the complete Q8_0 multiview bundle with SHA256 checks. It does not automatically claim other GGUF or TRELLIS repositories.

For a manually installed bundle, place the following files in `models/pixal3d/`:

```text
pixal3d-models.json
dinov3.gguf
pixal3d_naf.gguf
pixal3d_ss_flow_mv.gguf
ss_dec.gguf
pixal3d_shape_flow_512_mv.gguf
shape_dec.gguf
pixal3d_shape_flow_1024_mv.gguf
pixal3d_tex_flow_1024_mv.gguf
tex_dec.gguf
```

Use the matching upstream multiview manifest. Loading checks the manifest family, each required filename, the GGUF header, and the declared file size. Do not mix single-view, multiview, or TRELLIS components.

Create `models/pixal3d.yaml`:

```yaml
name: pixal3d
backend: pixal3dcpp
parameters:
  model: pixal3d
```

The model path names the **directory**, so distributed workers receive the complete model set. LocalAI also stages all four request images on the selected worker and removes them after the request.

### Generate

Select the Pixal3D model on Studio's **3D** page. Upload the four labelled views and enter the mesh scale. The form offers only supported generation controls.

The same request works through `POST /3d/generations`:

```json
{
  "model": "pixal3d",
  "images": [
    "data:image/png;base64,<front>",
    "data:image/png;base64,<right>",
    "data:image/png;base64,<back>",
    "data:image/png;base64,<left>"
  ],
  "mesh_scale": 0.8,
  "quality": "1024",
  "response_format": "url"
}
```

Replace the placeholders with the image data. `mesh_scale: 0.8` is an example, not an estimated default. `quality` can be omitted or set to `1024`; `background` can be omitted or set to `keep`. Both `url` and `b64_json` responses use the envelope described above.

Pixal3D rejects `image`, sampling overrides, nonempty `params`, and other resolutions or background modes. Other backends reject the new `images` and `mesh_scale` fields. The existing TRELLIS single-image request remains unchanged.

### Runtime and platforms

The adapter registers and unloads models through LocalAI's normal backend lifecycle. It starts the native CLI for each generation, so each request reloads weights. Unloading cancels an active child process. This adapter does not expose upstream's persistent HTTP server or its single-view mode.

Build definitions cover Linux amd64 and arm64 CPU/Vulkan, Linux amd64 CUDA 12/13, and Apple Silicon Metal. Some upstream operations use CPU fallbacks on Metal. CPU inference can be slow and requires substantial memory. Native builds and inference with real weights require platform validation; no throughput or memory guarantee is implied by the build definitions.
