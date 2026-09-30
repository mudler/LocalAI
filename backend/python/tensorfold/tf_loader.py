"""Load TensorFold in-process by running its own `serve` command.

Upstream has no embedding API, and its load path (drafter resolution, memory
budget, prefill plan, family checks) lives inside `cmd_serve`. Rather than copy
that, we run it and stop it at the point where it would start its HTTP server:
the MLX path builds `make_handler(app)`, the CUDA path calls `serve(app, ...)`.
Both are looked up at call time, so replacing them for the duration of the load
captures the constructed app. tests/test_embed_surface.py pins every symbol
touched here so an upstream refactor turns the bump build red.
"""
from __future__ import annotations

import contextlib
import os
import sys
from dataclasses import dataclass
from typing import Any, Callable

from tf_options import LoadConfig

MIN_CUDA_CAPABILITY = (9, 0)


class LoadError(Exception):
    """The model cannot be served here; the message is shown to the user."""


@dataclass
class Loaded:
    backend: str
    app: Any
    title: str


class _Captured(BaseException):
    """Unwinds cmd_serve once the app exists. BaseException so upstream's own
    `except Exception` handlers cannot swallow it."""

    def __init__(self, app: Any):
        self.app = app


def check_cuda_capability(capability: tuple[int, int]) -> None:
    if tuple(capability) < MIN_CUDA_CAPABILITY:
        raise LoadError(
            f"TensorFold's CUDA kernels need compute capability 9.0 or newer; this GPU is "
            f"{capability[0]}.{capability[1]}. Ampere and Ada GPUs are not supported."
        )


@contextlib.contextmanager
def _patched(obj: Any, name: str, value: Any):
    original = getattr(obj, name)
    setattr(obj, name, value)
    try:
        yield
    finally:
        setattr(obj, name, original)


def import_tensorfold():
    """Import TensorFold lazily so the rest of the backend loads without it."""
    from tensorfold import cli, families
    from tensorfold.server import http as http_module
    from tensorfold.server import stacks

    class _TF:
        pass

    tf = _TF()
    tf.cli, tf.families, tf.stacks, tf.http_module = cli, families, stacks, http_module

    def cuda_server_module():
        from tensorfold.cuda import server

        return server

    tf.cuda_server_module = cuda_server_module
    return tf


def load_app(
    cfg: LoadConfig,
    tf: Any,
    *,
    platform: str = sys.platform,
    cuda_capability: Callable[[], tuple[int, int]] | None = None,
) -> Loaded:
    os.environ.update(cfg.env)
    os.environ.setdefault("TENSORFOLD_NO_UPDATE_CHECK", "1")

    try:
        args = tf.cli.build_parser().parse_args(list(cfg.argv))
    except SystemExit as exc:
        raise LoadError(f"invalid tensorfold options (see the log for argparse's message): exit {exc.code}") from exc

    # Same first steps as cmd_serve; they read only config.json, so an
    # unsupported checkpoint is refused before any weight is downloaded.
    try:
        config_dir = tf.cli._config_dir(args.model)
        family = tf.families.detect(config_dir)
        backend = tf.cli._backend(args.backend, family)
    except Exception as exc:  # noqa: BLE001 - upstream raises ValueError, OSError, HTTP errors
        raise LoadError(str(exc) or type(exc).__name__) from exc

    if backend == "cuda":
        if cuda_capability is None:
            import torch

            cuda_capability = torch.cuda.get_device_capability
        check_cuda_capability(cuda_capability())

    captured: dict[str, Any] = {}

    def capture_mlx(app: Any):
        raise _Captured(app)

    def capture_cuda(app: Any, *_):
        captured["app"] = app

    with contextlib.ExitStack() as stack:
        # stacks.start/arm call signal.signal, which only works on the main
        # thread; the gRPC worker that runs LoadModel is not it.
        stack.enter_context(_patched(tf.stacks, "start", lambda: None))
        stack.enter_context(_patched(tf.stacks, "arm", lambda: None))
        if backend == "mlx":
            stack.enter_context(_patched(tf.http_module, "make_handler", capture_mlx))
        else:
            stack.enter_context(_patched(tf.cuda_server_module(), "serve", capture_cuda))
        try:
            tf.cli.cmd_serve(args)
        except _Captured as done:
            captured["app"] = done.app
        except Exception as exc:  # noqa: BLE001
            raise LoadError(str(exc) or type(exc).__name__) from exc

    if "app" not in captured:
        raise LoadError("TensorFold finished loading without building a server app")
    return Loaded(backend=backend, app=captured["app"], title=getattr(family, "title", ""))


def load_engine(cfg: LoadConfig):
    """Load the model and wrap it in the adapter for its backend."""
    tf = import_tensorfold()
    loaded = load_app(cfg, tf)
    if loaded.backend == "mlx":
        from tf_mlx import MlxEngine, import_helpers

        return MlxEngine(loaded.app, import_helpers())
    from tf_cuda import CudaEngine, import_helpers

    return CudaEngine(loaded.app, import_helpers())
