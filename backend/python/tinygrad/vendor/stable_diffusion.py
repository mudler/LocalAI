# Adapted from tinygrad examples/stable_diffusion.py (MIT license).
# Upstream: https://github.com/tinygrad/tinygrad/blob/master/examples/stable_diffusion.py
# Copyright (c) 2023- the tinygrad authors
# SPDX-License-Identifier: MIT
#
# Local modifications: removed the MLPerf training branch (pulls
# examples/mlperf/initializers which we don't vendor) and the __main__
# argparse / fetch / profile blocks. Kept the core classes so the LocalAI
# tinygrad backend can instantiate and drive Stable Diffusion v1.x from a
# single checkpoint path.
from collections import namedtuple
from typing import Any, Dict

import numpy as np
from tinygrad import Tensor, dtypes
from tinygrad.nn import Conv2d, GroupNorm

from . import clip as clip_mod
from . import unet as unet_mod
from .clip import Closed, Tokenizer
from .unet import UNetModel


class AttnBlock:
    def __init__(self, in_channels):
        self.norm = GroupNorm(32, in_channels)
        self.q = Conv2d(in_channels, in_channels, 1)
        self.k = Conv2d(in_channels, in_channels, 1)
        self.v = Conv2d(in_channels, in_channels, 1)
        self.proj_out = Conv2d(in_channels, in_channels, 1)

    def __call__(self, x):
        h_ = self.norm(x)
        q, k, v = self.q(h_), self.k(h_), self.v(h_)
        b, c, h, w = q.shape
        q, k, v = [t.reshape(b, c, h * w).transpose(1, 2) for t in (q, k, v)]
        h_ = Tensor.scaled_dot_product_attention(q, k, v).transpose(1, 2).reshape(b, c, h, w)
        return x + self.proj_out(h_)


class ResnetBlock:
    def __init__(self, in_channels, out_channels=None):
        self.norm1 = GroupNorm(32, in_channels)
        self.conv1 = Conv2d(in_channels, out_channels, 3, padding=1)
        self.norm2 = GroupNorm(32, out_channels)
        self.conv2 = Conv2d(out_channels, out_channels, 3, padding=1)
        self.nin_shortcut = Conv2d(in_channels, out_channels, 1) if in_channels != out_channels else (lambda x: x)

    def __call__(self, x):
        h = self.conv1(self.norm1(x).swish())
        h = self.conv2(self.norm2(h).swish())
        return self.nin_shortcut(x) + h


class Mid:
    def __init__(self, block_in):
        self.block_1 = ResnetBlock(block_in, block_in)
        self.attn_1 = AttnBlock(block_in)
        self.block_2 = ResnetBlock(block_in, block_in)

    def __call__(self, x):
        return x.sequential([self.block_1, self.attn_1, self.block_2])


class Decoder:
    def __init__(self):
        sz = [(128, 256), (256, 512), (512, 512), (512, 512)]
        self.conv_in = Conv2d(4, 512, 3, padding=1)
        self.mid = Mid(512)

        arr = []
        for i, s in enumerate(sz):
            arr.append({"block": [ResnetBlock(s[1], s[0]), ResnetBlock(s[0], s[0]), ResnetBlock(s[0], s[0])]})
            if i != 0:
                arr[-1]['upsample'] = {"conv": Conv2d(s[0], s[0], 3, padding=1)}
        self.up = arr

        self.norm_out = GroupNorm(32, 128)
        self.conv_out = Conv2d(128, 3, 3, padding=1)

    def __call__(self, x):
        x = self.conv_in(x)
        x = self.mid(x)
        for l in self.up[::-1]:
            for b in l['block']:
                x = b(x)
            if 'upsample' in l:
                bs, c, py, px = x.shape
                x = x.reshape(bs, c, py, 1, px, 1).expand(bs, c, py, 2, px, 2).reshape(bs, c, py * 2, px * 2)
                x = l['upsample']['conv'](x)
            x.realize()
        return self.conv_out(self.norm_out(x).swish())


class Encoder:
    def __init__(self):
        sz = [(128, 128), (128, 256), (256, 512), (512, 512)]
        self.conv_in = Conv2d(3, 128, 3, padding=1)

        arr = []
        for i, s in enumerate(sz):
            arr.append({"block": [ResnetBlock(s[0], s[1]), ResnetBlock(s[1], s[1])]})
            if i != 3:
                arr[-1]['downsample'] = {"conv": Conv2d(s[1], s[1], 3, stride=2, padding=(0, 1, 0, 1))}
        self.down = arr

        self.mid = Mid(512)
        self.norm_out = GroupNorm(32, 512)
        self.conv_out = Conv2d(512, 8, 3, padding=1)

    def __call__(self, x):
        x = self.conv_in(x)
        for l in self.down:
            for b in l['block']:
                x = b(x)
            if 'downsample' in l:
                x = l['downsample']['conv'](x)
        x = self.mid(x)
        return self.conv_out(self.norm_out(x).swish())


class AutoencoderKL:
    def __init__(self):
        self.encoder = Encoder()
        self.decoder = Decoder()
        self.quant_conv = Conv2d(8, 8, 1)
        self.post_quant_conv = Conv2d(4, 4, 1)

    def __call__(self, x):
        latent = self.encoder(x)
        latent = self.quant_conv(latent)
        latent = latent[:, 0:4]
        latent = self.post_quant_conv(latent)
        return self.decoder(latent)


def get_alphas_cumprod(beta_start=0.00085, beta_end=0.0120, n_training_steps=1000):
    betas = np.linspace(beta_start ** 0.5, beta_end ** 0.5, n_training_steps, dtype=np.float32) ** 2
    alphas = 1.0 - betas
    alphas_cumprod = np.cumprod(alphas, axis=0)
    return Tensor(alphas_cumprod)


# SD1.x UNet hyperparameters (same as upstream `unet_params`).
UNET_PARAMS_SD1: Dict[str, Any] = {
    "adm_in_ch": None,
    "in_ch": 4,
    "out_ch": 4,
    "model_ch": 320,
    "attention_resolutions": [4, 2, 1],
    "num_res_blocks": 2,
    "channel_mult": [1, 2, 4, 4],
    "n_heads": 8,
    "transformer_depth": [1, 1, 1, 1],
    "ctx_dim": 768,
    "use_linear": False,
}


class StableDiffusion:
    """Stable Diffusion 1.x pipeline, adapted from tinygrad's reference example.

    Drives the native CompVis `sd-v1-*.ckpt` checkpoint format (the only one
    the vendored weight layout handles). For HuggingFace safetensors pipelines
    the caller is expected to download / merge the `.ckpt` equivalent before
    calling LoadModel.
    """

    def __init__(self):
        self.alphas_cumprod = get_alphas_cumprod()
        self.first_stage_model = AutoencoderKL()
        self.cond_stage_model = namedtuple("CondStageModel", ["transformer"])(
            transformer=namedtuple("Transformer", ["text_model"])(text_model=Closed.ClipTextTransformer())
        )
        self.model = namedtuple("DiffusionModel", ["diffusion_model"])(
            diffusion_model=UNetModel(**UNET_PARAMS_SD1)
        )

    # DDIM update step.
    def _update(self, x, e_t, a_t, a_prev):
        sqrt_one_minus_at = (1 - a_t).sqrt()
        pred_x0 = (x - sqrt_one_minus_at * e_t) / a_t.sqrt()
        dir_xt = (1.0 - a_prev).sqrt() * e_t
        return a_prev.sqrt() * pred_x0 + dir_xt

    def _model_output(self, uncond, cond, latent, timestep, guidance):
        latents = self.model.diffusion_model(latent.expand(2, *latent.shape[1:]), timestep, uncond.cat(cond, dim=0))
        uncond_latent, cond_latent = latents[0:1], latents[1:2]
        return uncond_latent + guidance * (cond_latent - uncond_latent)

    def step(self, uncond, cond, latent, timestep, a_t, a_prev, guidance):
        e_t = self._model_output(uncond, cond, latent, timestep, guidance)
        return self._update(latent, e_t, a_t, a_prev).realize()

    def decode(self, x):
        x = self.first_stage_model.post_quant_conv(1 / 0.18215 * x)
        x = self.first_stage_model.decoder(x)
        x = (x + 1.0) / 2.0
        x = x.reshape(3, 512, 512).permute(1, 2, 0).clip(0, 1) * 255
        return x.cast(dtypes.uint8)

    def encode_prompt(self, tokenizer, prompt: str):
        ids = Tensor([tokenizer.encode(prompt)])
        return self.cond_stage_model.transformer.text_model(ids).realize()


def run_sd15(model: StableDiffusion, prompt: str, negative_prompt: str, steps: int, guidance: float, seed: int):
    """Generate a single 512x512 image. Returns a (512,512,3) uint8 tensor."""
    tokenizer = Tokenizer.ClipTokenizer()

    context = model.encode_prompt(tokenizer, prompt)
    uncond = model.encode_prompt(tokenizer, negative_prompt)

    timesteps = list(range(1, 1000, 1000 // steps))
    alphas = model.alphas_cumprod[Tensor(timesteps)]
    alphas_prev = Tensor([1.0]).cat(alphas[:-1])

    if seed is not None:
        Tensor.manual_seed(seed)
    latent = Tensor.randn(1, 4, 64, 64)

    for index in range(len(timesteps) - 1, -1, -1):
        timestep = timesteps[index]
        tid = Tensor([index])
        latent = model.step(
            uncond, context, latent,
            Tensor([timestep]),
            alphas[tid], alphas_prev[tid],
            Tensor([guidance]),
        )

    return model.decode(latent).realize()
