# Debugging and Rebuilding Backends

When a backend fails at runtime (e.g. a gRPC method error, a Python import error, or a dependency conflict), use this guide to diagnose, fix, and rebuild.

## Architecture Overview

- **Source directory**: `backend/python/<name>/` (or `backend/go/<name>/`, `backend/cpp/<name>/`)
- **Installed directory**: `backends/<name>/` — this is what LocalAI actually runs. It is populated by `make backends/<name>` which builds a Docker image, exports it, and installs it via `local-ai backends install`.
- **Virtual environment**: `backends/<name>/venv/` — the installed Python venv (for Python backends). The Python binary is at `backends/<name>/venv/bin/python`.

Editing files in `backend/python/<name>/` does **not** affect the running backend until you rebuild with `make backends/<name>`.

## Diagnosing Failures

### 1. Check the logs

Backend gRPC processes log to LocalAI's stdout/stderr. Look for lines tagged with the backend's model ID:

```
GRPC stderr id="trl-finetune-127.0.0.1:37335" line="..."
```

Common error patterns:
- **"Method not implemented"** — the backend is missing a gRPC method that the Go side calls. The model loader (`pkg/model/initializers.go`) always calls `LoadModel` after `Health`; fine-tuning backends must implement it even as a no-op stub.
- **Python import errors / `AttributeError`** — usually a dependency version mismatch (e.g. `pyarrow` removing `PyExtensionType`).
- **"failed to load backend"** — the gRPC process crashed or never started. Check stderr lines for the traceback.

### 2. Test the Python environment directly

You can run the installed venv's Python to check imports without starting the full server:

```bash
backends/<name>/venv/bin/python -c "import datasets; print(datasets.__version__)"
```

If `pip` is missing from the venv, bootstrap it:

```bash
backends/<name>/venv/bin/python -m ensurepip
```

Then use `backends/<name>/venv/bin/python -m pip install ...` to test fixes in the installed venv before committing them to the source requirements.

### 3. Check upstream dependency constraints

When you hit a dependency conflict, check what the main library expects. For example, TRL's upstream `requirements.txt`:

```
https://github.com/huggingface/trl/blob/main/requirements.txt
```

Pin minimum versions in the backend's requirements files to match upstream.

## Common Fixes

### Missing gRPC methods

If the Go side calls a method the backend doesn't implement (e.g. `LoadModel`), add a no-op stub in `backend.py`:

```python
def LoadModel(self, request, context):
    """No-op — actual loading happens elsewhere."""
    return backend_pb2.Result(success=True, message="OK")
```

The gRPC contract requires `LoadModel` to succeed for the model loader to return a usable client, even if the backend doesn't need upfront model loading.

### Dependency version conflicts

Python backends often break when a transitive dependency releases a breaking change (e.g. `pyarrow` removing `PyExtensionType`). Steps:

1. Identify the broken import in the logs
2. Test in the installed venv: `backends/<name>/venv/bin/python -c "import <module>"`
3. Check upstream requirements for version constraints
4. Update **all** requirements files in `backend/python/<name>/`:
   - `requirements.txt` — base deps (grpcio, protobuf)
   - `requirements-cpu.txt` — CPU-specific (includes PyTorch CPU index)
   - `requirements-cublas12.txt` — CUDA 12
   - `requirements-cublas13.txt` — CUDA 13
5. Rebuild: `make backends/<name>`

### PyTorch index conflicts (uv resolver)

The Docker build uses `uv` for pip installs. When `--extra-index-url` points to the PyTorch wheel index, `uv` may refuse to fetch packages like `requests` from PyPI if it finds a different version on the PyTorch index first. Fix this by adding `--index-strategy=unsafe-first-match` to `install.sh`:

```bash
EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
installRequirements
```

Most Python backends already do this — check `backend/python/transformers/install.sh` or similar for reference.

## Rebuilding

### Rebuild a single backend

```bash
make backends/<name>
```

This runs the Docker build (`Dockerfile.python`), exports the image to `backend-images/<name>.tar`, and installs it into `backends/<name>/`. It also rebuilds the `local-ai` Go binary (without extra tags).

**Important**: If you were previously running with `GO_TAGS=auth`, the `make backends/<name>` step will overwrite your binary without that tag. Rebuild the Go binary afterward:

```bash
GO_TAGS=auth make build
```

### Rebuild and restart

After rebuilding a backend, you must restart LocalAI for it to pick up the new backend files. The backend gRPC process is spawned on demand when the model is first loaded.

```bash
# Kill existing process
kill <pid>

# Restart
./local-ai run --debug [your flags]
```

### Quick iteration (skip Docker rebuild)

For fast iteration on a Python backend's `backend.py` without a full Docker rebuild, you can edit the installed copy directly:

```bash
# Edit the installed copy
vim backends/<name>/backend.py

# Restart LocalAI to respawn the gRPC process
```

This is useful for testing but **does not persist** — the next `make backends/<name>` will overwrite it. Always commit fixes to the source in `backend/python/<name>/`.

## Verification

After fixing and rebuilding:

1. Start LocalAI and confirm the backend registers: look for `Registering backend name="<name>"` in the logs
2. Trigger the operation that failed (e.g. start a fine-tuning job)
3. Watch the GRPC stderr/stdout lines for the backend's model ID
4. Confirm no errors in the traceback
