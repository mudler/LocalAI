---
title: "Why we write our own C and C++ engines"
date: 2026-07-24
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["engineering", "ggml", "vllm.cpp", "depth-anything.cpp", "parity"]
summary: "Eighteen of our backends are C or C++ ports we wrote from scratch instead of wrapping an upstream engine. Here is why we did it and what we measured."
extracss: ["blog.css"]
---

Most LocalAI backends wrap somebody else's engine. llama.cpp, vLLM, whisper.cpp, stable-diffusion and MLX are maintained by people who work on those models full time, and wrapping one of them costs us a Dockerfile and a gRPC shim. We do that wherever we can.

Eighteen of our backends do not wrap anything. They are C or C++ ports we wrote from scratch, and each one exists because wrapping the upstream engine would have meant shipping something we could not ship: a multi-gigabyte Python install, a CUDA-only stack that will not run on half the machines our users have, or, in a few cases, a model with no C++ implementation to wrap in the first place. Below are the numbers for four of them, and what keeping them alive takes.

## vllm.cpp: 66 MiB instead of 9.1 GiB

Deploying a Python inference stack means resolving a dependency tree at install time, on the target machine, against whatever CUDA and glibc that machine has. Deploying a ggml port means copying a shared library and a GGUF file.

[vllm.cpp](https://github.com/mudler/vllm.cpp) is our C++20 port of vLLM's V1 serving architecture. Installing vLLM produces a 9.1 GiB virtualenv. Installing vllm.cpp produces a 66 MiB binary. It implements the same things the Python original does, including paged KV cache, continuous batching, prefix caching, the scheduler and the sampler, with no Python, no PyTorch and no ggml at inference.

The question is what that does to throughput. On an NVIDIA GB10 running Qwen3.6-27B in NVFP4, greedy, closed loop, against vLLM in its production graphed configuration rather than `--enforce-eager`:

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

Those are ties. Our run-to-run noise band is 0.5%, and concurrency 2 through 32 land between 0.7% and 1.7%, so those five points sit inside the noise or close enough to it not to matter. Only the single-stream case, at 4.5%, is clearly outside. Output is token-for-token identical to vLLM at every point on the curve, and peak host memory is 24.88 GiB against 28.18 GiB.

The install drops from 9.1 GiB to 66 MiB and the throughput stays where it was, which is what we were after.

Against llama.cpp on CPU from the same GGUF file, prefill runs 1.18x faster (223.8 against 177.3 tok/s), decode is a tie inside llama.cpp's own spread, and the tokens match its greedy decode exactly. Against MLX-LM on an Apple M4, prefill time to first token is 1.5% ahead and warm total throughput is 97.6% of MLX-LM, a real 2.4% gap that sits entirely in decode.

## depth-anything.cpp is faster on CPU

[depth-anything.cpp](https://github.com/mudler/depth-anything.cpp) is a port of ByteDance's Depth Anything 3, which gives you metric depth in metres from one ordinary photo, plus per-pixel confidence, camera intrinsics and extrinsics, and a back-projected point cloud. On CPU it runs faster than PyTorch on the same model.

<div class="tw">
<table>
<thead><tr><th>Engine</th><th>Quant</th><th>Model MB</th><th>Load ms</th><th>Infer ms</th><th>Peak RAM MB</th><th>vs PyTorch</th></tr></thead>
<tbody>
<tr><td>PyTorch</td><td>f32</td><td>516</td><td>749</td><td>416.9</td><td>1328</td><td>1.00x</td></tr>
<tr><td><b>C++/ggml</b></td><td>q8_0</td><td>142</td><td><b>40</b></td><td><b>319.4</b></td><td><b>363</b></td><td><b>1.31x</b></td></tr>
</tbody>
</table>
</div>

That is on a Ryzen 9 9950X3D at 504x336 with 16 threads. The C++ build runs the same model 1.31x faster, uses 363 MB of RAM against 1328 MB, and loads in 40 ms instead of 749 ms. The quantized q4_k build is a 99 MB file and stays near-lossless. Output correlates 1.0 with the reference forward pass across 37 parity tests.

We did not write a better matmul kernel than PyTorch. Two positional embeddings, the DPT head's UV embedding and the backbone's bicubic position embedding, were being recomputed on every forward pass with single-threaded scalar sin, cos and bicubic loops, even though they depend only on the input geometry and are identical every call. Caching them removed about 95 ms of host-side overhead per forward, which is most of the gap. PyTorch builds the same embeddings with vectorized operations and never had that overhead to begin with.

The heavy GEMMs are close to a wash, because everyone is calling into the same class of BLAS kernel. What is left is host-side work that a Python reference implementation never bothered to optimize, plus not loading an interpreter and a framework to do inference. On GPU it goes back to parity: with the ggml CUDA backend and flash attention on a GB10, depth-anything.cpp ties PyTorch's tuned cuDNN at 47.3 ms per forward, and wins only the cold start, loading 1.75x to 2.9x faster.

## The two where we are slower

[face-detect.cpp](https://github.com/mudler/face-detect.cpp) and [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) replaced LocalAI's Python `insightface` and `speaker-recognition` backends. Neither of them is faster than what it replaced on CPU, and we shipped them anyway.

face-detect.cpp runs the whole insightface buffalo chain, so SCRFD detection, five-landmark similarity-transform alignment to 112x112, and the ArcFace embedding, out of one self-contained GGUF with no Python and no onnxruntime. Detector boxes and landmarks match insightface to within 1 pixel, and the recognition embedding matches to cosine 1.000000 at any thread count. On CPU it is slower than onnxruntime: SCRFD detect runs at about 0.83x at one thread and 0.69x at eight, ArcFace embed at about 0.61x and 0.84x. onnxruntime's MLAS convolution kernels sit at the FMA-port peak, and a custom AVX2 Winograd path narrowed the gap without closing it. On GPU, routing the same convolutions through cuDNN takes SCRFD from 14.8 ms to 6.4 ms, which lands at torch-cuDNN parity.

voice-detect.cpp has a memory result instead. A WeSpeaker verification peaks at about 62 MB in our binary against about 334 MB for the CPU-only Python, torch and onnxruntime path, roughly 5.4x lower, with an identical verdict and embedding cosine 1.000000. End to end on CPU the two land within 10 to 15% of each other, trading the lead by model and thread count, and on GPU the conv encoders match the reference.

For a biometric pipeline we would rather have the exact match than the speed. An embedding that differs in the fourth decimal place changes verification decisions at a threshold, and every enrolled template in a deployment would have to be recomputed. Matching insightface exactly is what lets somebody swap the backend out without re-enrolling their users.

## How we do it

Every port follows the same four steps.

Convert the weights first, into one GGUF with the tokenizer, the vocabulary and any auxiliary model embedded, so that deploying the model is copying a file.

Port the graph second, and check it component by component against reference tensors dumped from the original implementation. depth-anything.cpp has 37 ctest cases covering preprocessing, backbone, attention, the DPT head, depth, pose, the ray head, the ray to pose solver and the exporters. parakeet.cpp checks transcript agreement with NeMo at WER 0. face-detect.cpp checks box and landmark distance in pixels, and embedding cosine. Skip this step and you find out the port is wrong months later, from a user, on a model you had stopped thinking about.

Optimize third, with a profiler, and only once the parity checks pass. In parakeet.cpp the win was caching a prediction-network LSTM forward pass that was 97% of transducer decode time and mostly redundant. In depth-anything.cpp it was the two positional embeddings above. Neither was a kernel rewrite, and neither would have turned up without a working baseline to profile.

Expose a flat C ABI last. LocalAI dlopens the shared library through purego and calls that ABI directly, so there is no subprocess, no gRPC hop to a Python server, and no interpreter in the serving path.

## What it takes to maintain

Each engine is its own repository with its own CI, benchmark suite, GGUF conversion script and parity baselines, and upstream keeps releasing checkpoints that need converter work.

GPU kernels are the weak spot. ggml's generic CUDA convolution and attention kernels trail NVIDIA's tuned cuDNN on the conv-heavy models, which is why face-detect.cpp needs an explicit cuDNN path to reach parity, and why parakeet.cpp's GPU margin over NeMo is a median 1.25x while its CPU margin is wider.

It also does not scale to everything. llama.cpp, vLLM, whisper.cpp, MLX and diffusers stay wrapped, because those projects are large, fast-moving and already good at what they do. We write an engine when a model has no C++ implementation, when the Python dependency is heavier than the model itself, or when the thing we need does not exist yet. The rest we install like everybody else.

One thing that confuses people reading the tree for the first time: LocalAI's own core is Go, and each backend is written in whatever its model's ecosystem needs, which is why there is C++ sitting next to Python in the same repository.

Every engine above keeps its benchmark suite, its parity checks and its methodology in its own repository, including the runs that did not work out. The full list is the "Backends built by us" table in the [LocalAI README](https://github.com/mudler/LocalAI#backends-built-by-us).
