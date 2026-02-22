# LocalAI Diffusers Backend

This backend provides gRPC access to Hugging Face diffusers pipelines with dynamic pipeline loading.

## Creating a separate environment for the diffusers project

```
make diffusers
```

## Dynamic Pipeline Loader

The diffusers backend includes a dynamic pipeline loader (`diffusers_dynamic_loader.py`) that automatically discovers and loads diffusers pipelines at runtime. This eliminates the need for per-pipeline conditional statements - new pipelines added to diffusers become available automatically without code changes.

### How It Works

1. **Pipeline Discovery**: On first use, the loader scans the `diffusers` package to find all classes that inherit from `DiffusionPipeline`.

2. **Registry Caching**: Discovery results are cached for the lifetime of the process to avoid repeated scanning.

3. **Task Aliases**: The loader automatically derives task aliases from class names (e.g., "text-to-image", "image-to-image", "inpainting") without hardcoding.

4. **Multiple Resolution Methods**: Pipelines can be resolved by:
   - Exact class name (e.g., `StableDiffusionPipeline`)
   - Task alias (e.g., `text-to-image`, `img2img`)
   - Model ID (uses HuggingFace Hub to infer pipeline type)

### Usage Examples

```python
from diffusers_dynamic_loader import (
    load_diffusers_pipeline,
    get_available_pipelines,
    get_available_tasks,
    resolve_pipeline_class,
    discover_diffusers_classes,
    get_available_classes,
)

# List all available pipelines
pipelines = get_available_pipelines()
print(f"Available pipelines: {pipelines[:10]}...")

# List all task aliases
tasks = get_available_tasks()
print(f"Available tasks: {tasks}")

# Resolve a pipeline class by name
cls = resolve_pipeline_class(class_name="StableDiffusionPipeline")

# Resolve by task alias
cls = resolve_pipeline_class(task="stable-diffusion")

# Load and instantiate a pipeline
pipe = load_diffusers_pipeline(
    class_name="StableDiffusionPipeline",
    model_id="runwayml/stable-diffusion-v1-5",
    torch_dtype=torch.float16
)

# Load from single file
pipe = load_diffusers_pipeline(
    class_name="StableDiffusionPipeline",
    model_id="/path/to/model.safetensors",
    from_single_file=True,
    torch_dtype=torch.float16
)

# Discover other diffusers classes (schedulers, models, etc.)
schedulers = discover_diffusers_classes("SchedulerMixin")
print(f"Available schedulers: {list(schedulers.keys())[:5]}...")

# Get list of available scheduler classes
scheduler_list = get_available_classes("SchedulerMixin")
```

### Generic Class Discovery

The dynamic loader can discover not just pipelines but any class type from diffusers:

```python
# Discover all scheduler classes
schedulers = discover_diffusers_classes("SchedulerMixin")

# Discover all model classes
models = discover_diffusers_classes("ModelMixin")

# Get a sorted list of available classes
scheduler_names = get_available_classes("SchedulerMixin")
```

### Special Pipeline Handling

Most pipelines are loaded dynamically through `load_diffusers_pipeline()`. Only pipelines requiring truly custom initialization logic are handled explicitly:

- `FluxTransformer2DModel`: Requires quantization and custom transformer loading (cannot use dynamic loader)
- `WanPipeline` / `WanImageToVideoPipeline`: Uses dynamic loader with special VAE (float32 dtype)
- `SanaPipeline`: Uses dynamic loader with post-load dtype conversion for VAE/text encoder
- `StableVideoDiffusionPipeline`: Uses dynamic loader with CPU offload handling
- `VideoDiffusionPipeline`: Alias for DiffusionPipeline with video flags

All other pipelines (StableDiffusionPipeline, StableDiffusionXLPipeline, FluxPipeline, etc.) are loaded purely through the dynamic loader.

### Error Handling

When a pipeline cannot be resolved, the loader provides helpful error messages listing available pipelines and tasks:

```
ValueError: Unknown pipeline class 'NonExistentPipeline'. 
Available pipelines: AnimateDiffPipeline, AnimateDiffVideoToVideoPipeline, ...
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COMPEL` | `0` | Enable Compel for prompt weighting |
| `XPU` | `0` | Enable Intel XPU support |
| `CLIPSKIP` | `1` | Enable CLIP skip support |
| `SAFETENSORS` | `1` | Use safetensors format |
| `CHUNK_SIZE` | `8` | Decode chunk size for video |
| `FPS` | `7` | Video frames per second |
| `DISABLE_CPU_OFFLOAD` | `0` | Disable CPU offload |
| `FRAMES` | `64` | Number of video frames |
| `BFL_REPO` | `ChuckMcSneed/FLUX.1-dev` | Flux base repo |
| `PYTHON_GRPC_MAX_WORKERS` | `1` | Max gRPC workers |

## Running Tests

```bash
./test.sh
```

The test suite includes:
- Unit tests for the dynamic loader (`test_dynamic_loader.py`)
- Integration tests for the gRPC backend (`test.py`)