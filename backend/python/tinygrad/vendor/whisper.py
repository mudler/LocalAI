# Adapted from tinygrad examples/whisper.py (MIT license).
# Upstream: https://github.com/tinygrad/tinygrad/blob/master/examples/whisper.py
# Copyright (c) 2023- the tinygrad authors
# SPDX-License-Identifier: MIT
#
# Local modifications: removed the pyaudio listener / __main__ block; the rest
# is the core Whisper model + preprocessing + single-file transcription path.
from __future__ import annotations

import base64
import collections
import itertools
from typing import List, Literal, Optional, Union

import numpy as np
from tinygrad import Tensor, TinyJit, Variable, dtypes, nn
from tinygrad.helpers import fetch
from tinygrad.nn.state import load_state_dict, torch_load

from .audio_helpers import mel


class MultiHeadAttention:
    def __init__(self, n_state, n_head, kv_caching: Literal['cross', 'self', None] = None, max_self_attn_cache_len=None):
        self.n_head = n_head
        self.query = nn.Linear(n_state, n_state)
        self.key = nn.Linear(n_state, n_state, bias=False)
        self.value = nn.Linear(n_state, n_state)
        self.out = nn.Linear(n_state, n_state)
        self.kv_caching = kv_caching
        self.max_self_attn_cache_len = max_self_attn_cache_len

    def __call__(self, x, xa=None, mask=None, len=None):
        if self.kv_caching == 'cross':
            if xa is not None:
                k, v = self.key(xa), self.value(xa)
                if not hasattr(self, 'cache_k'):
                    self.cache_k, self.cache_v = k, v
                else:
                    self.cache_k.assign(k).realize()
                    self.cache_v.assign(v).realize()
            else:
                k, v = self.cache_k, self.cache_v
        else:
            k, v = self.key(x), self.value(x)
            if self.kv_caching == 'self':
                if not hasattr(self, 'cache_k'):
                    self.cache_k = Tensor.zeros(x.shape[0], self.max_self_attn_cache_len, x.shape[2])
                    self.cache_v = Tensor.zeros(x.shape[0], self.max_self_attn_cache_len, x.shape[2])
                k = self.cache_k.shrink((None, (0, len), None)).cat(k, dim=1)
                v = self.cache_v.shrink((None, (0, len), None)).cat(v, dim=1)
                padding = self.max_self_attn_cache_len - len - x.shape[1]
                self.cache_k.assign(k.pad((None, (0, padding), None)).contiguous()).realize()
                self.cache_v.assign(v.pad((None, (0, padding), None)).contiguous()).realize()

        q = self.query(x)
        n_ctx = q.shape[1]
        head_dim = q.shape[-1] // self.n_head
        q = q.reshape(*q.shape[:2], self.n_head, head_dim).permute(0, 2, 1, 3)
        k = k.reshape(*k.shape[:2], self.n_head, head_dim).permute(0, 2, 1, 3)
        v = v.reshape(*v.shape[:2], self.n_head, head_dim).permute(0, 2, 1, 3)
        attn = Tensor.scaled_dot_product_attention(q, k, v, mask[:n_ctx, :n_ctx] if mask is not None else None)
        wv = attn.permute(0, 2, 1, 3).flatten(start_dim=2)
        return self.out(wv)


class ResidualAttentionBlock:
    def __init__(self, n_state, n_head, is_decoder_block=False, max_self_attn_cache_len=None):
        self.attn = MultiHeadAttention(n_state, n_head, kv_caching='self' if is_decoder_block else None, max_self_attn_cache_len=max_self_attn_cache_len)
        self.attn_ln = nn.LayerNorm(n_state)
        self.cross_attn = MultiHeadAttention(n_state, n_head, kv_caching='cross') if is_decoder_block else None
        self.cross_attn_ln = nn.LayerNorm(n_state) if is_decoder_block else None
        self.mlp = [nn.Linear(n_state, n_state * 4), Tensor.gelu, nn.Linear(n_state * 4, n_state)]
        self.mlp_ln = nn.LayerNorm(n_state)

    def __call__(self, x, xa=None, mask=None, len=None):
        x = x + self.attn(self.attn_ln(x), mask=mask, len=len)
        if self.cross_attn:
            x = x + self.cross_attn(self.cross_attn_ln(x), xa)
        x = x + self.mlp_ln(x).sequential(self.mlp)
        return x.realize()


class AudioEncoder:
    def __init__(self, n_mels, n_audio_ctx, n_audio_state, n_audio_head, n_audio_layer, **_):
        self.conv1 = nn.Conv1d(n_mels, n_audio_state, kernel_size=3, padding=1)
        self.conv2 = nn.Conv1d(n_audio_state, n_audio_state, kernel_size=3, stride=2, padding=1)
        self.blocks = [ResidualAttentionBlock(n_audio_state, n_audio_head) for _ in range(n_audio_layer)]
        self.ln_post = nn.LayerNorm(n_audio_state)
        self.positional_embedding = Tensor.empty(n_audio_ctx, n_audio_state)
        self.encode = TinyJit(self.__call__)

    def __call__(self, x):
        x = self.conv1(x).gelu()
        x = self.conv2(x).gelu()
        x = x.permute(0, 2, 1)
        x = x + self.positional_embedding[:x.shape[1]]
        x = x.sequential(self.blocks)
        x = self.ln_post(x)
        return x.realize()


class TextDecoder:
    def __init__(self, n_vocab, n_text_ctx, n_text_state, n_text_head, n_text_layer, **_):
        self.max_tokens_to_sample = n_text_ctx // 2
        self.max_self_attn_cache_len = n_text_ctx
        self.token_embedding = nn.Embedding(n_vocab, n_text_state)
        self.positional_embedding = Tensor.empty(n_text_ctx, n_text_state)
        self.blocks = [ResidualAttentionBlock(n_text_state, n_text_head, is_decoder_block=True, max_self_attn_cache_len=self.max_self_attn_cache_len) for _ in range(n_text_layer)]
        self.ln = nn.LayerNorm(n_text_state)
        self.mask = Tensor.full((n_text_ctx, n_text_ctx), -np.inf).triu(1).realize()
        self.getjitted = collections.defaultdict(lambda: TinyJit(self.forward))

    def __call__(self, x, pos, encoded_audio):
        pos = Variable("self_attn_cache_len", 1, self.max_self_attn_cache_len - 1).bind(pos) if pos else 0
        return self.getjitted[x.shape](x, pos, encoded_audio)

    def forward(self, x, pos, encoded_audio):
        seqlen = x.shape[-1]
        x = self.token_embedding(x) + self.positional_embedding.shrink(((pos, pos + seqlen), None))
        for block in self.blocks:
            x = block(x, xa=encoded_audio, mask=self.mask, len=pos)
        return self.output_tok(x)

    def output_tok(self, x):
        return (self.ln(x) @ self.token_embedding.weight.T).realize()


class Whisper:
    def __init__(self, dims, batch_size=1):
        self.encoder = AudioEncoder(**dims)
        self.decoder = TextDecoder(**dims)
        self.is_multilingual = dims["n_vocab"] == 51865
        self.batch_size = batch_size


RATE = 16000
SEGMENT_SECONDS = 30
SAMPLES_PER_SEGMENT = RATE * SEGMENT_SECONDS
N_FFT = 400
HOP_LENGTH = 160
N_MELS = 80
FRAMES_PER_SEGMENT = SAMPLES_PER_SEGMENT // HOP_LENGTH


def prep_audio(waveforms: List[np.ndarray], batch_size: int, truncate: bool = False) -> np.ndarray:
    import librosa

    def pad_or_trim(arr, target_len):
        if len(arr) == target_len:
            return arr
        if len(arr) < target_len:
            return np.pad(arr, (0, target_len - len(arr)), 'constant')
        return arr[:target_len]

    max_len = SAMPLES_PER_SEGMENT if truncate else max(len(w) for w in waveforms)
    if (r := max_len % SAMPLES_PER_SEGMENT) > 0:
        max_len += SAMPLES_PER_SEGMENT - r

    waveforms = np.array(list(map(lambda w: pad_or_trim(w, max_len), waveforms)))
    if waveforms.shape[0] < batch_size:
        waveforms = np.pad(waveforms, pad_width=((0, batch_size - waveforms.shape[0]), (0, 0)))

    stft = librosa.stft(waveforms, n_fft=N_FFT, hop_length=HOP_LENGTH, window='hann', dtype=np.csingle)
    magnitudes = np.absolute(stft[..., :-1]) ** 2
    mel_spec = mel(sr=RATE, n_fft=N_FFT, n_mels=N_MELS).numpy() @ magnitudes
    log_spec = np.log10(np.clip(mel_spec, 1e-10, None))
    log_spec = np.maximum(log_spec, log_spec.max((1, 2), keepdims=True) - 8.0)
    log_spec = (log_spec + 4.0) / 4.0
    return log_spec


LANGUAGES = {
    "en": "english", "zh": "chinese", "de": "german", "es": "spanish", "ru": "russian", "ko": "korean",
    "fr": "french", "ja": "japanese", "pt": "portuguese", "tr": "turkish", "pl": "polish", "it": "italian",
}


def get_encoding(encoding_name: str):
    import tiktoken

    with fetch(f"https://raw.githubusercontent.com/openai/whisper/main/whisper/assets/{encoding_name}.tiktoken").open() as f:
        ranks = {base64.b64decode(token): int(rank) for token, rank in (line.split() for line in f if line)}
    n_vocab = len(ranks)
    specials = [
        "<|endoftext|>",
        "<|startoftranscript|>",
        *[f"<|{lang}|>" for lang in LANGUAGES.keys()],
        "<|translate|>",
        "<|transcribe|>",
        "<|startoflm|>",
        "<|startofprev|>",
        "<|nospeech|>",
        "<|notimestamps|>",
        *[f"<|{i * 0.02:.2f}|>" for i in range(1501)],
    ]
    special_tokens = dict(zip(specials, itertools.count(n_vocab)))
    return tiktoken.Encoding(
        name=encoding_name,
        explicit_n_vocab=n_vocab + len(specials),
        pat_str=r"""'s|'t|'re|'ve|'m|'ll|'d| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+(?!\S)|\s+""",
        mergeable_ranks=ranks,
        special_tokens=special_tokens,
    )


MODEL_URLS = {
    "tiny.en": "https://openaipublic.azureedge.net/main/whisper/models/d3dd57d32accea0b295c96e26691aa14d8822fac7d9d27d5dc00b4ca2826dd03/tiny.en.pt",
    "tiny": "https://openaipublic.azureedge.net/main/whisper/models/65147644a518d12f04e32d6f3b26facc3f8dd46e5390956a9424a650c0ce22b9/tiny.pt",
    "base.en": "https://openaipublic.azureedge.net/main/whisper/models/25a8566e1d0c1e2231d1c762132cd20e0f96a85d16145c3a00adf5d1ac670ead/base.en.pt",
    "base": "https://openaipublic.azureedge.net/main/whisper/models/ed3a0b6b1c0edf879ad9b11b1af5a0e6ab5db9205f891f668f8b0e6c6326e34e/base.pt",
    "small.en": "https://openaipublic.azureedge.net/main/whisper/models/f953ad0fd29cacd07d5a9eda5624af0f6bcf2258be67c92b79389873d91e0872/small.en.pt",
    "small": "https://openaipublic.azureedge.net/main/whisper/models/9ecf779972d90ba49c06d968637d720dd632c55bbf19d441fb42bf17a411e794/small.pt",
}


def init_whisper(model_name: str = "base", batch_size: int = 1):
    filename = fetch(MODEL_URLS[model_name])
    state = torch_load(filename)
    model = Whisper(state['dims'], batch_size)
    load_state_dict(model, state['model_state_dict'], strict=False)
    enc = get_encoding("multilingual" if model.is_multilingual else "gpt2")
    return model, enc


def load_file_waveform(filename: str):
    import librosa
    waveform, _ = librosa.load(filename, sr=RATE)
    return waveform


def transcribe_waveform(model: Whisper, enc, waveforms, language: Optional[str] = None, truncate: bool = False) -> str:
    log_spec = prep_audio(waveforms, model.batch_size, truncate)
    nsample = model.decoder.max_tokens_to_sample
    nctx = model.decoder.max_self_attn_cache_len

    start_tokens = [enc._special_tokens["<|startoftranscript|>"]]
    if model.is_multilingual:
        lang = language if (language and language in LANGUAGES) else "en"
        language_token = enc._special_tokens["<|startoftranscript|>"] + 1 + tuple(LANGUAGES.keys()).index(lang)
        start_tokens.append(language_token)
        start_tokens.append(enc._special_tokens["<|transcribe|>"])
    start_tokens.append(enc._special_tokens["<|notimestamps|>"])

    eot = enc._special_tokens["<|endoftext|>"]

    def inferloop(ctx, encoded_audio):
        pos, next_tokens = 0, ctx
        for _ in range(nsample):
            next_tokens = model.decoder(Tensor(next_tokens, dtype=dtypes.int32), pos, encoded_audio)[:, -1].argmax(axis=-1).numpy().astype(np.int32).reshape(-1, 1)
            next_tokens[ctx[:, -1] == eot] = eot
            ctx = np.concatenate((ctx, next_tokens), axis=1)
            pos = ctx.shape[-1] - 1
            if (next_tokens == eot).all() or pos == nctx:
                break
        return ctx

    ctx = np.tile(start_tokens, (model.batch_size, 1))
    transcriptions: list[list[int]] = [[] for _ in waveforms]

    for curr_frame in range(0, log_spec.shape[-1], FRAMES_PER_SEGMENT):
        encoded_audio = model.encoder.encode(Tensor(log_spec[:, :, curr_frame:curr_frame + FRAMES_PER_SEGMENT]))
        ctx_arr = inferloop(np.array(ctx), encoded_audio)
        for i, arr in enumerate(ctx_arr):
            if i >= len(waveforms):
                break
            end_idxs = np.where(arr == eot)[0]
            start_idx = np.where(arr == start_tokens[-1])[0][0] + 1
            end_idx = end_idxs[0] if len(end_idxs) else None
            transcriptions[i].extend(arr[start_idx:end_idx])
        ctx = ctx_arr

    texts = [enc.decode([int(t) for t in toks]).strip() for toks in transcriptions]
    return texts[0] if len(texts) == 1 else "\n".join(texts)
