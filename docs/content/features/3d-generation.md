+++
disableToc = false
title = "3D Generation"
weight = 19
url = "/features/3d-generation/"
+++

LocalAI can generate textured 3D meshes from a single conditioning image via the `/3d/generations` endpoint, powered by the `trellis2cpp` backend — a C++/GGML port of [Microsoft TRELLIS.2](https://github.com/microsoft/TRELLIS.2) ([trellis2.cpp](https://github.com/localai-org/trellis2cpp)). The output is a binary glTF (`.glb`) asset with PBR materials.

Generation is image-conditioned only — there is no text-prompt path. Provide a photo or rendering of a single object (ideally on a plain background) and TRELLIS.2 reconstructs a full 3D mesh from it.

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

For 3D printing, `params.print_remesh: "true"` wraps the generated mesh with CGAL Alpha Wrap into a watertight, oriented, intersection-free 2-manifold; when the source is textured, the PBR material is re-projected per texel onto the wrap's UV atlas. `alpha_ratio` (default `0.005`) sets the smallest preserved detail and `offset_ratio` (default `alpha_ratio / 30`) the shell standoff, both as fractions of the mesh bounding-box diagonal. Smaller `alpha_ratio` keeps more detail but produces more triangles and takes longer.

### Response

Returns a JSON response using LocalAI's OpenAI-style generation envelope:

| Field             | Type     | Description                                                    |
|-------------------|----------|----------------------------------------------------------------|
| `created`         | `int`    | Unix timestamp of generation                                   |
| `id`              | `string` | Unique identifier (UUID)                                       |
| `data`            | `array`  | Array with the generated asset                                 |
| `data[].url`      | `string` | URL path to the `.glb` under `/generated-3d` (if `url`)        |
| `data[].b64_json` | `string` | Base64-encoded GLB (if `response_format` is `b64_json`)        |

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

The React UI includes a 3D tab in the Studio (and a `/3d` page) with an interactive PBR viewer: upload an image, pick the quality, and preview the generated mesh with orbit/pan/zoom and a wireframe toggle. Past generations are kept in the browser (IndexedDB) with a GLB download button.

## Notes

- The 512³ pipeline takes roughly two minutes on a modern GPU; the 1024³ cascade takes around five minutes and needs about 10 GB VRAM plus a temporary host-RAM spike.
- `TRELLIS2_DEVICE=cpu` forces CPU inference (slow; mainly for debugging).
- The generated mesh has unoriented winding (faithful to TRELLIS.2) and is exported Y-up with vertex-PBR materials; a UV-atlas texture bake can be enabled in the backend via the `T2GLB_XATLAS` environment variable.
