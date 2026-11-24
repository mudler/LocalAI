---
title: "parakeet.cpp: the same NeMo transcript, without the Python"
date: 2026-06-05
author: "Ettore Di Giacinto"
category: "Benchmarks"
tags: ["parakeet.cpp", "ASR", "ggml", "streaming", "benchmarks"]
summary: "The same transcript as NVIDIA NeMo at a median 1.40x on CPU, and about 27x the speed of whisper.cpp, from one binary and one GGUF file."
extracss: ["blog.css"]
---

You can drop a single binary and a GGUF file onto a machine with no GPU and get NVIDIA NeMo Parakeet transcription out of it, at a median 1.40x NeMo's own PyTorch CPU speed, with the same transcript NeMo produces. That is [parakeet.cpp](https://github.com/mudler/parakeet.cpp), a C++17 port of the Parakeet speech-recognition family built on ggml.

We checked the accuracy before touching the speed, because a transcriber that disagrees with the reference is not a port of it.

## WER 0 against NeMo

Every published checkpoint is validated at WER 0 against NeMo. Across the LibriSpeech test-clean set the mean f32 agreement WER, meaning the word error rate between our transcript and NeMo's on the same audio, is 0.0155%. On seven of the ten models it is exactly 0.0000%, which is a byte-identical transcript.

Both engines did the same work and produced the same output, so the only difference left is how long they took.

## CPU, against NeMo's own runtime

Measured on a 20-core x86 host with 8 threads for both engines, LibriSpeech test-clean, batch size 1 on both sides. NeMo 2.7.3 on PyTorch CPU is the reference. RTFx is audio seconds divided by processing seconds, so higher is faster.

<div class="tw">
<table>
<thead><tr><th>Model</th><th>RTFx NeMo</th><th>RTFx f32</th><th>Speedup f32</th><th>Speedup q8_0</th><th>Agreement WER</th><th>RSS NeMo</th><th>RSS ours</th></tr></thead>
<tbody>
<tr><td>ctc-0.6b</td><td>30.7</td><td>41.7</td><td>1.36x</td><td>1.61x</td><td>0.0000%</td><td>5447 MB</td><td>2457 MB</td></tr>
<tr><td>ctc-1.1b</td><td>17.8</td><td>26.1</td><td>1.46x</td><td>1.63x</td><td>0.0222%</td><td>8914 MB</td><td>4201 MB</td></tr>
<tr><td>rnnt-0.6b</td><td>24.3</td><td>34.1</td><td>1.40x</td><td>1.52x</td><td>0.0000%</td><td>5516 MB</td><td>2487 MB</td></tr>
<tr><td>rnnt-1.1b</td><td>15.3</td><td>21.4</td><td>1.40x</td><td>1.57x</td><td>0.0000%</td><td>8982 MB</td><td>4231 MB</td></tr>
<tr><td>tdt-0.6b-v2</td><td>22.4</td><td>32.4</td><td>1.45x</td><td>1.55x</td><td>0.0000%</td><td>5499 MB</td><td>2545 MB</td></tr>
<tr><td>tdt-1.1b</td><td>14.6</td><td>22.6</td><td>1.54x</td><td>1.74x</td><td>0.0000%</td><td>8956 MB</td><td>4231 MB</td></tr>
<tr><td>tdt_ctc-1.1b</td><td>13.3</td><td>22.5</td><td><b>1.69x</b></td><td><b>1.89x</b></td><td>0.0985%</td><td>8909 MB</td><td>4236 MB</td></tr>
<tr><td>tdt_ctc-110m</td><td>72.9</td><td>81.1</td><td>1.11x</td><td>1.26x</td><td>0.0196%</td><td>1650 MB</td><td>563 MB</td></tr>
<tr><td>rt-eou-120m-v1</td><td>61.8</td><td>70.6</td><td>1.14x</td><td>1.23x</td><td>0.0000%</td><td>1714 MB</td><td>621 MB</td></tr>
</tbody>
</table>
</div>

<figure>
  <img src="/img/parakeet-speedup.jpg" alt="Bar chart of parakeet.cpp CPU speedup over NeMo, per model and per GGUF dtype" loading="lazy">
  <figcaption>CPU speedup over NeMo per model and per dtype, from the benchmark suite that runs in CI. Values above 1.0 mean parakeet.cpp finished first on the same audio with the same transcript.</figcaption>
</figure>

The range across all ten models is 1.11x to 1.69x at f32, with a median of 1.40x. Quantizing to q8_0 shrinks the file to 37% of f32 and pushes the best case to 1.86x, staying near-lossless. f16 is 57% of the size and reaches 1.70x. K-quants below that keep shrinking the model at a small and monotonic accuracy cost, which the per-model tables in the repository lay out.

Peak resident memory is roughly half NeMo's on every model, and lower again once quantized. The 110M model runs in 563 MB against NeMo's 1650 MB, which is the difference between fitting on a small edge box and not.

Against whisper.cpp turbo on the same clip and at the same accuracy (1.6% WER on that audio), parakeet.cpp is about 27x faster on CPU and about 12x faster on GPU. That gap is mostly architectural: Whisper is an encoder-decoder that processes fixed 30 second windows, and Parakeet's FastConformer plus transducer decodes only the frames it has.

## Where the CPU speed came from

The decisive win was on the decode side. A transducer decodes autoregressively, and profiling showed the prediction-network LSTM taking about 97% of RNN-T decode time while producing the same output over and over: on a non-emitting frame the prediction network's input has not changed, so its forward pass is redundant. Caching that forward across non-emitting frames removed most of the decode cost.

The encoder side is a set of smaller wins: a persistent ggml backend with `gallocr`, zero-copy weights straight out of the GGUF mapping, one fused graph rather than per-layer graph building, and tinyBLAS through `GGML_LLAMAFILE`.

## On the GPU

On an NVIDIA GB10 (Grace-Blackwell), parakeet.cpp wins on all ten models, with a median of 1.25x and up to 4.3x on the large TDT and hybrid models. The reference here is NeMo-GPU inside the `nvcr.io/nvidia/nemo` container, because NeMo cannot run on that host's torch and CUDA stack directly.

The 4.3x cases have a specific cause. NeMo's TDT greedy decode is not CUDA-graph accelerated and falls back to a per-step Python loop, while ours is a lean C++ loop. Where NeMo's decode is CUDA-graph accelerated, as it is for RNN-T, the gap narrows to about 1.16x at f32 and 1.30x at q8_0. On the pure-encoder CTC models the margin is around 1.2x, because ggml's generic CUDA conv and attention kernels still trail NVIDIA's tuned cuDNN. That is the main piece of GPU headroom left in the project, and the README lists it per model.

Batching several clips through the decoder together reaches about 10x to 12x at batch size 16 on the GB10, and about 3x to 5x on CPU. It applies to transducer models only, since CTC has no autoregressive decode to batch, and the batched path produces the same output as running the clips one at a time.

On Apple M4 through ggml's Metal backend, the larger models run about 3x to 5x faster than the same models on that machine's CPU.

## Cache-aware streaming and end-of-utterance detection

Offline transcription hands you a file and waits. A voice assistant cannot do that, so `parakeet_realtime_eou_120m-v1` runs a cache-aware streaming path instead: you feed it 16 kHz mono PCM as it arrives and it returns newly finalized text as it becomes stable.

Cache-aware means the cost per chunk stays flat. Each chunk's forward pass carries per-layer convolution and attention caches plus the transducer decoder state forward, so nothing before the current chunk is recomputed. Without that, every chunk would re-run the encoder over the whole session so far, and the per-chunk cost would grow with the length of the conversation until the loop fell behind. The implementation covers layer norm with causal convolution, causal subsampling, and chunked-limited attention, and its transcript matches NeMo's own cache-aware streaming exactly.

End-of-utterance detection changes how an assistant feels. The model emits `<EOU>` when the speaker has finished a turn and `<EOB>` for a backchannel, as events alongside the text. A voice loop can start generating a reply the moment `<EOU>` arrives rather than waiting out a fixed silence timer, which is where most of the perceived lag in a spoken assistant comes from. The alternative, a VAD with a 700 ms hangover, either cuts people off mid-sentence or makes the assistant feel slow, and it cannot tell "mm-hm" from the end of a thought. `finalize` flushes the tail at end of stream without fabricating an `<EOU>` that NeMo would not have emitted.

The streaming path measures at RTFx 3.80 on a 7.43 second clip. That sits well below the offline number by design, because streaming runs many small chunked passes rather than one large one, and it is still several times faster than real time on a CPU.

There is also a multilingual streaming model, `nemotron-3.5-asr-streaming-0.6b`, covering 40 or more locales with a per-language prompt. On CPU it runs at 2.40x NeMo at f32 and 2.52x at q8_0, with agreement WER 0.0000% in both cases, offline and streaming.

## Long audio without the memory cliff

The FastConformer encoder uses global relative-position self-attention, which is O(T squared) in time and in memory. A 16.6 minute file subsamples to roughly 12,000 encoder frames, and the score and mask tensors alone reach tens of gigabytes, which is enough to take down a node.

parakeet.cpp ports NeMo's `rel_pos_local_attn`, a banded attention where each query attends only to keys within a window, making attention O(T times W). It turns on automatically past 8192 encoder frames, and `PARAKEET_ATT_CONTEXT` forces a specific window.

<div class="tw">
<table>
<thead><tr><th>Attention</th><th>Window</th><th>Wall clock</th><th>RTFx</th><th>Peak RSS</th></tr></thead>
<tbody>
<tr><td>full (global)</td><td>-</td><td>148.3 s</td><td>6.7x</td><td>54.0 GB</td></tr>
<tr><td>banded</td><td>W=32</td><td>39.5 s</td><td>25.2x</td><td>8.9 GB</td></tr>
<tr><td><b>banded</b></td><td><b>W=128</b></td><td><b>36.9 s</b></td><td><b>27.0x</b></td><td><b>9.4 GB</b></td></tr>
</tbody>
</table>
</div>

At NeMo's full W=128 window that is about 4x faster and about 5.7x less peak memory than the global path. The band is built with a chunk-matmul construction, overlapping key and value chunks feeding one batched GEMM plus a diagonal skew view, so the graph node count does not depend on the window. The wide window costs the same as the narrow one. Short clips stay on the global path and produce the same output as before.

## Using it

parakeet.cpp ships prebuilt `parakeet-cli` bundles for Linux x64 (CPU, Vulkan, CUDA), Linux arm64, macOS arm64 with Metal, macOS x64 and Windows x64. In LocalAI it is the `parakeet-cpp` backend, which dlopens `libparakeet.so` through purego and calls the C ABI directly, so there is no Python process in the serving path and transcription comes back on the standard OpenAI-compatible endpoint.

The full benchmark suite, methodology and per-model plots are in [benchmarks/BENCHMARK.md](https://github.com/mudler/parakeet.cpp/blob/main/benchmarks/BENCHMARK.md), and the parity matrix per checkpoint is in `docs/parity.md`.
