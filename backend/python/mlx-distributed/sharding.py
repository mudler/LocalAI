"""
Auto-parallelism for MLX distributed inference.

Provides pipeline parallelism (Ring backend) by wrapping model layers with
distributed send/recv operations.  Ported from exo's auto_parallel.py with
simplifications for LocalAI's use case.
"""
import mlx.core as mx
import mlx.nn as nn


class PipelineFirstLayer(nn.Module):
    """Wraps the first layer on each rank to receive from the previous rank."""

    def __init__(self, original_layer, rank, group):
        super().__init__()
        dict.__setitem__(self, "_original_layer", original_layer)
        self.rank = rank
        self.group = group

    @property
    def original_layer(self):
        return self["_original_layer"]

    def __getattr__(self, name):
        try:
            return super().__getattr__(name)
        except AttributeError:
            return getattr(self["_original_layer"], name)

    def __call__(self, x, *args, **kwargs):
        if self.rank != 0:
            mx.eval(x)
            x = mx.distributed.recv_like(x, self.rank - 1, group=self.group)
            mx.eval(x)
        return self.original_layer(x, *args, **kwargs)


class PipelineLastLayer(nn.Module):
    """Wraps the last layer on each rank to send to the next rank."""

    def __init__(self, original_layer, rank, world_size, group):
        super().__init__()
        dict.__setitem__(self, "_original_layer", original_layer)
        self.rank = rank
        self.world_size = world_size
        self.group = group

    @property
    def original_layer(self):
        return self["_original_layer"]

    def __getattr__(self, name):
        try:
            return super().__getattr__(name)
        except AttributeError:
            return getattr(self["_original_layer"], name)

    def __call__(self, x, *args, **kwargs):
        output = self.original_layer(x, *args, **kwargs)
        mx.eval(output)
        if self.rank != self.world_size - 1:
            output = mx.distributed.send(
                output, (self.rank + 1) % self.world_size, group=self.group
            )
            mx.eval(output)
        # Gather output from all ranks so every rank has the final result
        output = mx.distributed.all_gather(output, group=self.group)[
            -output.shape[0] :
        ]
        mx.eval(output)
        return output


def get_inner_model(model):
    """Get the inner model (model.model or model.transformer)."""
    for attr in ("model", "transformer"):
        inner = getattr(model, attr, None)
        if isinstance(inner, nn.Module):
            # Some models have model.model (e.g. language_model.model)
            inner_inner = getattr(inner, "model", None)
            if isinstance(inner_inner, nn.Module):
                return inner_inner
            return inner
    raise ValueError("Model must have a 'model' or 'transformer' attribute")


def get_layers(inner_model):
    """Get the list of transformer layers."""
    for attr in ("layers", "h"):
        layers = getattr(inner_model, attr, None)
        if layers is not None:
            return layers
    raise ValueError("Model must have a 'layers' or 'h' attribute")


def pipeline_auto_parallel(model, group, start_layer=None, end_layer=None):
    """Apply pipeline parallelism to a model.

    Each rank only keeps its slice of layers.  The first layer receives from
    the previous rank, and the last layer sends to the next rank.

    Args:
        model: The MLX model (must have model.layers or similar)
        group: The distributed group
        start_layer: First layer index for this rank (auto-computed if None)
        end_layer: Last layer index (exclusive) for this rank (auto-computed if None)
    """
    rank = group.rank()
    world_size = group.size()

    inner = get_inner_model(model)
    layers = list(get_layers(inner))
    total_layers = len(layers)

    if start_layer is None or end_layer is None:
        layers_per_rank = total_layers // world_size
        remainder = total_layers % world_size
        start_layer = rank * layers_per_rank + min(rank, remainder)
        end_layer = start_layer + layers_per_rank + (1 if rank < remainder else 0)

    layers = layers[start_layer:end_layer]
    for layer in layers:
        mx.eval(layer)

    # Wrap first and last layers
    layers[0] = PipelineFirstLayer(layers[0], rank, group=group)
    layers[-1] = PipelineLastLayer(layers[-1], rank, world_size, group=group)

    # Replace layers on the inner model
    if hasattr(inner, "layers"):
        inner.layers = layers
    elif hasattr(inner, "h"):
        inner.h = layers

    return model
