---
title: "Why we write our own C and C++ engines"
date: 2026-07-24
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["engineering", "ggml", "vllm.cpp", "depth-anything.cpp", "parity"]
summary: "A 66 MiB binary instead of a 9.1 GiB virtualenv, depth estimation that beats PyTorch on CPU in half the memory, and biometrics that match insightface bit for bit. The method, the measurements, and what it costs us."
extracss: ["blog.css"]
---

Most LocalAI backends wrap somebody else's engine, and that is the right default. llama.cpp, vLLM, whisper.cpp, stable-diffusion, MLX and the rest are maintained by people who are better at those models than we are, and wrapping them costs a Dockerfile and a gRPC shim.

Eighteen of our backends do not wrap anything. They are C or C++ ports we wrote from scratch, and each one exists because wrapping the upstream engine would have meant shipping something we could not ship: a multi-gigabyte Python install, a non-portable CUDA-only stack, or a model that had no C++ implementation at all. This post is about what those ports buy, measured, and what they cost.

## What you get: one file, and memory you can predict

Deploying a Python inference stack means resolving a dependency tree at install time, on the target machine, against whatever CUDA and glibc it has. Deploying a ggml port means copying a shared library and a GGUF file.

The clearest measurement of that difference is [vllm.cpp](https://github.com/mudler/vllm.cpp), our C++20 port of vLLM's V1 serving architecture. Installing vLLM produces a 9.1 GiB virtualenv. Installing vllm.cpp produces a 66 MiB binary. The engine implements the same things the Python original does, including paged KV cache, continuous batching, prefix caching, the scheduler and the sampler, with no Python, no PyTorch and no ggml at inference.

The obvious question is what that costs in throughput. On an NVIDIA GB10 running Qwen3.6-27B in NVFP4, greedy, closed loop, against vLLM in its production graphed configuration rather than `--enforce-eager`:

<div class="tw">
<table>
<thead><tr><th>Concurrency</th><th>1</th><th>2</th><th>4</th><th>8</th><th>16</th><th>32</th></tr></thead>
<tbody>
<tr><td><b>vllm.cpp</b> tok/s</td><td><b>86.05</b></td><td><b>159.68</b></td><td><b>292.34</b></td><td><b>508.77</b></td><td><b>801.76</b></td><td><b>1095.01</b></td></tr>
<tr><td>vLLM tok/s</td><td>82.32</td><td>158.03</td><td>290.31</td><td>505.46</td><td>789.16</td><td>1076.25</td></tr>
<tr><td>Ratio</td><td>1.045x</td><td>1.011x</td><td>1.007x</td><td>1.007x</td><td>1.016x</td><td>1.017x</td></tr>
</tbody>
</table>
</div>

We are ahead at all six points, and five of those six are ties. Our run-to-run noise band is 0.5%, and concurrency 2 through 32 land between 0.7% and 1.7%, so the honest reading is that only the single-stream case (4.5%) is clearly outside noise. Output is token-for-token identical to vLLM at every point on that curve. Peak host memory is 24.88 GiB against 28.18 GiB.

A tie against a mature CUDA stack is a good result for a 66 MiB binary, and it means the footprint saving is not paid for in throughput. Against llama.cpp on CPU from the same GGUF file, prefill runs 1.18x faster (223.8 against 177.3 tok/s), decode is a tie inside llama.cpp's own spread, and the tokens are byte-identical to its greedy decode. Against MLX-LM on an Apple M4, prefill time to first token is 1.5% ahead and warm total throughput is 97.6% of MLX-LM, a real 2.4% gap that sits entirely in decode.

## Sometimes the port is simply faster

[depth-anything.cpp](https://github.com/mudler/depth-anything.cpp) is a port of ByteDance's Depth Anything 3, which gives you metric depth in metres from one ordinary photo, plus per-pixel confidence, camera intrinsics and extrinsics, and a back-projected point cloud. On CPU it is faster than PyTorch running the same model.

<div class="tw">
<table>
<thead><tr><th>Engine</th><th>Quant</th><th>Model MB</th><th>Load ms</th><th>Infer ms</th><th>Peak RAM MB</th><th>vs PyTorch</th></tr></thead>
<tbody>
<tr><td>PyTorch</td><td>f32</td><td>516</td><td>749</td><td>416.9</td><td>1328</td><td>1.00x</td></tr>
<tr><td><b>C++/ggml</b></td><td>q8_0</td><td>142</td><td><b>40</b></td><td><b>319.4</b></td><td><b>363</b></td><td><b>1.31x</b></td></tr>
</tbody>
</table>
</div>

Same model, 1.31x the speed, 27% of the memory, and a load that finishes in 40 ms instead of 749 ms, on a Ryzen 9 9950X3D at 504x336 with 16 threads. The quantized q4_k build is a 99 MB file and stays near-lossless. Output correlates 1.0 with the reference forward pass, component by component, across 37 parity tests.

The reason it is faster has nothing to do with writing better matmul kernels than PyTorch. Two positional embeddings, the DPT head's UV embedding and the backbone's bicubic position embedding, were being recomputed on every forward pass with single-threaded scalar sin, cos and bicubic loops, even though they depend only on the input geometry and are identical every call. Caching them removed about 95 ms of host-side overhead per forward, which is most of the gap. PyTorch builds the same embeddings with vectorized operations and never paid that cost.

That is the general shape of these wins. The heavy GEMMs are close to a wash, because everyone is calling into the same class of BLAS kernel. The difference sits in host-side work that a Python reference implementation never bothered to optimize, and in not loading an interpreter and a framework to do inference. On GPU the picture flips back to parity: with the ggml CUDA backend and flash attention on a GB10, depth-anything.cpp ties PyTorch's tuned cuDNN at 47.3 ms per forward, and wins only the cold start, loading 1.75x to 2.9x faster.

## Parity is the gate, speed is the follow-up

[face-detect.cpp](https://github.com/mudler/face-detect.cpp) and [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) replaced LocalAI's Python `insightface` and `speaker-recognition` backends. Both are the case where we do not claim a CPU speed win, and both shipped anyway.

face-detect.cpp runs the whole insightface buffalo chain, so SCRFD detection, five-landmark similarity-transform alignment to 112x112, and the ArcFace embedding, out of one self-contained GGUF with no Python and no onnxruntime. Detector boxes and landmarks match insightface to within 1 pixel, and the recognition embedding matches to cosine 1.000000, held at any thread count. On CPU it is slower than onnxruntime: SCRFD detect runs at about 0.83x at one thread and 0.69x at eight, ArcFace embed at about 0.61x and 0.84x. onnxruntime's MLAS convolution kernels sit at the FMA-port peak, and a custom AVX2 Winograd path narrowed the gap without closing it. On GPU, routing the same convolutions through cuDNN takes SCRFD from 14.8 ms to 6.4 ms and lands at torch-cuDNN parity.

voice-detect.cpp is the same story with a memory result attached. A WeSpeaker verification peaks at about 62 MB in our binary against about 334 MB for the CPU-only Python, torch and onnxruntime path, roughly 5.4x lower, with an identical verdict and embedding cosine 1.000000. End to end on CPU the two land within 10 to 15% of each other, trading the lead by model and thread count, and on GPU the conv encoders match the reference.

For a biometric pipeline, matching the reference exactly matters more than being faster than it. An embedding that differs in the fourth decimal place changes verification decisions at a threshold, and every enrolled template in a deployment would have to be recomputed. Parity is what makes the replacement a drop-in rather than a migration.

## The method

Every port follows the same sequence, and the order is the important part.

Convert the weights first, into one GGUF with the tokenizer, the vocabulary and any auxiliary model embedded, so that deploying the model is copying a file.

Port the graph second, and gate it component by component against reference tensors dumped from the original implementation. depth-anything.cpp has 37 ctest cases covering preprocessing, backbone, attention, the DPT head, depth, pose, the ray head, the ray to pose solver and the exporters. parakeet.cpp gates on transcript agreement with NeMo at WER 0. face-detect.cpp gates on box and landmark distance in pixels and embedding cosine. A port that is fast and slightly wrong is worthless, and without a per-component gate you find out it is wrong months later.

Optimize third, with a profiler, and only after parity holds. In parakeet.cpp the decisive win was caching a prediction-network LSTM forward pass that was 97% of transducer decode time and mostly redundant. In depth-anything.cpp it was two cached positional embeddings. Neither was a kernel rewrite, and neither would have been findable without a working baseline to profile.

Expose a flat C ABI last. LocalAI dlopens the shared library through purego and calls that ABI directly, so there is no subprocess, no gRPC hop to a Python server, and no interpreter in the serving path.

## What it costs

Maintenance, mostly. Each engine is a repository with its own CI, its own benchmark suite, its own GGUF conversion script and its own parity baselines, and upstream keeps releasing new checkpoints that need converter work.

GPU kernels are the weak spot. ggml's generic CUDA convolution and attention kernels trail NVIDIA's tuned cuDNN on the conv-heavy models, which is why face-detect.cpp needs an explicit cuDNN path to reach parity, and why parakeet.cpp's GPU margin over NeMo is a median 1.25x while its CPU margin is wider.

Porting also does not scale to everything. llama.cpp, vLLM, whisper.cpp, MLX and diffusers stay wrapped, because those projects are large, fast-moving and already excellent at what they do. We write an engine when a model has no C++ implementation, when the Python dependency is heavier than the model, or when the thing we need does not exist yet. Everything else we install from somebody else.

Every engine listed above keeps its own benchmark suite, its parity gates and its methodology in its own repository, including the runs that did not work. The full list of them is the "Backends built by us" table in the [LocalAI README](https://github.com/mudler/LocalAI#backends-built-by-us).
