# MLX-Video on Apple Silicon

Install the backend and start LocalAI with the example model configuration:

```bash
local-ai backends install mlx-video
local-ai run --models-path ./examples/mlx-video
```

Generate a short video:

```bash
curl http://localhost:8080/video \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "ltx2-mlx",
    "prompt": "Ocean waves at golden hour, locked camera",
    "width": 768,
    "height": 512,
    "num_frames": 49,
    "fps": 24
  }'
```

MLX-Video and its models require substantial unified memory. Reduce the canvas
and frame count first if generation exhausts memory.
