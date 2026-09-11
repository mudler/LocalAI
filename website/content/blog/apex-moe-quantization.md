---
title: "APEX: a 35B MoE model at 12.2 GB, and faster than F16"
date: 2026-04-10
author: "Ettore Di Giacinto"
category: "Research"
tags: ["quantization", "APEX", "mixture-of-experts", "llama.cpp", "benchmarks"]
summary: "Qwen3.5-35B-A3B goes from 64.6 GB to 12.2 GB and speeds up from 30.4 to 74.4 tokens per second. Perplexity moves from 6.537 to 7.088. Here is the precision assignment that does it, and where the quality drops."
extracss: ["blog.css"]
---

A 35B mixture-of-experts model at full precision is a 64.6 GB file, which puts it out of reach of every consumer GPU. APEX gets Qwen3.5-35B-A3B down to 12.2 GB, where it fits a 16 GB card with room for context, and it generates at 74.4 tokens per second instead of 30.4. The output is an ordinary GGUF that stock llama.cpp opens with no patches and no custom build.

At that tier the quality does drop, and the numbers below say by how much. At the 21.3 GB tier it barely drops at all: APEX Quality has a lower perplexity than the F16 model it was quantized from.

## The measurements

All of this is Qwen3.5-35B-A3B on an NVIDIA DGX Spark (GB10, 122 GB unified VRAM, CUDA 13). Perplexity is wikitext-2-raw at context 2048 over the full test set. HellaSwag and the other accuracy benchmarks run through llama.cpp at 400 tasks. `tg128` is token generation throughput.

<div class="tw">
<table>
<thead><tr><th>Build</th><th>Size</th><th>Perplexity</th><th>KL mean</th><th>HellaSwag</th><th>MMLU</th><th>tg128 t/s</th></tr></thead>
<tbody>
<tr><td>F16</td><td>64.6 GB</td><td>6.537</td><td>-</td><td>82.5%</td><td>41.5%</td><td>30.4</td></tr>
<tr><td>Q8_0</td><td>34.4 GB</td><td>6.533</td><td>0.0046</td><td>83.0%</td><td>41.2%</td><td>52.5</td></tr>
<tr><td>Unsloth UD-Q8_K_XL</td><td>45.3 GB</td><td>6.536</td><td>0.0025</td><td>82.5%</td><td>41.3%</td><td>36.4</td></tr>
<tr><td>Unsloth UD-Q4_K_XL</td><td>20.7 GB</td><td>6.554</td><td>0.0097</td><td>83.0%</td><td>40.6%</td><td>58.1</td></tr>
<tr><td>bartowski IQ2_M</td><td>11.3 GB</td><td>7.303</td><td>0.1113</td><td>80.3%</td><td>39.6%</td><td>76.2</td></tr>
<tr><td><b>APEX Quality</b></td><td><b>21.3 GB</b></td><td><b>6.527</b></td><td>0.0114</td><td>83.0%</td><td>41.2%</td><td><b>62.3</b></td></tr>
<tr><td><b>APEX I-Quality</b></td><td><b>21.3 GB</b></td><td>6.552</td><td>0.0102</td><td><b>83.5%</b></td><td>41.4%</td><td>63.1</td></tr>
<tr><td><b>APEX Balanced</b></td><td>23.6 GB</td><td>6.533</td><td>0.0088</td><td>83.0%</td><td>41.3%</td><td>60.8</td></tr>
<tr><td><b>APEX Compact</b></td><td>16.1 GB</td><td>6.783</td><td>0.0469</td><td>82.5%</td><td>40.9%</td><td>69.8</td></tr>
<tr><td><b>APEX I-Compact</b></td><td>16.1 GB</td><td>6.669</td><td>0.0332</td><td>81.8%</td><td>41.7%</td><td>69.8</td></tr>
<tr><td><b>APEX Mini</b></td><td><b>12.2 GB</b></td><td>7.088</td><td>0.0870</td><td>81.0%</td><td>41.3%</td><td><b>74.4</b></td></tr>
</tbody>
</table>
</div>

Three things in that table are worth stopping on.

APEX Quality is 21.3 GB, a third of F16, and its perplexity of 6.527 is lower than F16's 6.537 and lower than Q8_0's 6.533. Quantization noise acting as mild regularization on a wikitext evaluation is a known effect and we are not claiming the quantized model is smarter. At this tier the loss is below the measurement floor.

Against Unsloth's UD-Q8_K_XL, APEX I-Quality is half the size (21.3 GB against 45.3 GB), one point ahead on HellaSwag (83.5% against 82.5%), within 0.016 on perplexity, and 73% faster (63.1 t/s against 36.4).

At the bottom end, APEX Mini beats bartowski IQ2_M on every metric while being 0.9 GB larger: perplexity 7.088 against 7.303, HellaSwag 81.0% against 80.3%, MMLU 41.3% against 39.6%.

## Why it also gets faster

Token generation on a single stream is bound by memory bandwidth, not by arithmetic. Every generated token requires reading the active weights out of memory, so halving the bytes roughly halves the time spent waiting for them. Going from 64.6 GB to 12.2 GB takes throughput from 30.4 to 74.4 tokens per second, a 2.45x gain on the same hardware with the same kernels. Every APEX tier clears 60 t/s.

That is also why a large well-behaved quant such as UD-Q8_K_XL is slower than a smaller one with equal quality.

## Per-tensor and per-layer precision

Uniform quantization gives every tensor the same bit width, which spends the same precision on a weight that fires for every token and one that fires for 3% of them. In a mixture-of-experts model those two populations are enormous and they are easy to tell apart.

APEX classifies every tensor into one of three roles and treats them differently.

**Routed expert weights** (the gate, up and down projections inside the experts) are the bulk of the parameters, and only 8 of 256 experts are active per token. That 97% structural sparsity is why aggressive quantization is safe here. The routing decision itself reads full-precision gate weights, so quantization noise inside an expert that was not selected never reaches the output at all. When an expert is selected, its contribution is one of eight summed paths, which further dilutes per-tensor error.

**Shared expert weights** run for every single token and their weight distribution is heavy-tailed, with a kurtosis of 13.10 against 3.41 for routed experts. Those outliers carry real signal and low-bit formats clip them. Q8_0 is the minimum viable precision here, and dropping it degrades the build quickly.

**Attention and SSM weights** are dense, contribute few parameters relative to the experts, and matter for generation quality. They sit at Q6_K throughout.

On top of the role split there is a layer-wise gradient. The first and last five transformer layers do input embedding alignment and output logit generation, and they are measurably more sensitive than the middle layers, which perform more redundant intermediate processing. So routed experts get Q6_K at the edges (L0-4 and L35-39), Q5_K near the edges (L5-9 and L30-34), and Q4_K or IQ4_XS in the middle (L10-29). The tiers differ mostly in how far down that middle band is pushed: Compact runs Q4_K edges with Q3_K middle, and Mini goes to IQ2_S in the middle.

None of this needs a patched llama.cpp. The assignments are expressed with the stock `--tensor-type-file` flag for per-layer rules and `--tensor-type` for component-level ones.

## What the experiments ruled out

Twenty-five or so systematic runs produced a few results that saved a lot of time later.

Going from Q6_K to Q8_0 on routed experts costs 7.5 GB and gives zero perplexity improvement. Going below Q5_K on them causes measurable degradation. Q6_K is the ceiling.

Layer position matters more than uniform bit width. A two-tier gradient of Q6_K edges and Q5_K middle matches Q8_0 quality; a uniform Q5_K assignment at a similar size does not.

IQ formats underperform K-quants on MoE experts. IQ3_S gives worse perplexity than Q3_K on routed expert tensors at a similar bit rate, because the near-Gaussian expert weight distribution (kurtosis 3.41) suits the K-quant block structure better.

Five C-level modifications to the quantization algorithms themselves, including error feedback, enhanced scale search, super-block refinement and Gaussian-density weighting, all showed zero improvement. Stock llama.cpp quantization is already good. The gains here come entirely from deciding where to put the bits.

## The I-variants and their calibration set

Standard imatrix calibration uses Wikipedia text, which is also what wikitext perplexity measures, so the calibration and the benchmark agree with each other by construction. The I-variants calibrate on a diverse set spanning chat, code, reasoning and tool-calling, with no Wikipedia in it.

It shows up in the numbers. I-Compact drops perplexity from 6.783 to 6.669, cuts KL max from 7.56 to 5.50, and lifts MMLU from 40.9% to 41.7%. At the Quality tier, I-Quality gives up 0.025 perplexity against Quality and takes the highest HellaSwag score of anything tested (83.5%), the best TruthfulQA (38.4%), and a lower KL divergence. If your workload is chat, code or agents rather than encyclopedic prose, take the I variant.

## Where the quality drops

The Compact and Mini tiers lose real quality.

Compact at 16.1 GB moves perplexity from 6.537 to 6.783, a 3.8% increase, and its KL mean rises tenfold against Q8_0, from 0.0046 to 0.0469. Mini at 12.2 GB goes to 7.088, an 8.4% increase, with a KL mean of 0.0870 and HellaSwag down 1.5 points to 81.0%. Those are the numbers to weigh against the fact that the model now runs at all on a 16 GB card.

The accuracy benchmarks in that table run at 400 tasks, so a single point of difference sits inside the noise. MMLU in particular stays between 39.6% and 41.7% across every build including F16, which says more about the resolution of a 400-task evaluation than about the quantizations. Perplexity and KL divergence are the metrics that separate these builds cleanly, and KL max is the one that shows worst-case outlier behaviour rather than the average.

Running the accuracy evaluations at all required fixing llama.cpp's hybrid memory path for recurrent architectures, since Qwen3.5-35B-A3B uses both attention and SSM blocks and the evaluations crashed before the fix. That went upstream as [llama.cpp #21224](https://github.com/ggml-org/llama.cpp/pull/21224).

## Running one

The GGUFs are published in the [APEX collection on Hugging Face](https://huggingface.co/collections/mudler/apex-quants) and open in any current llama.cpp build. With LocalAI:

```bash
local-ai run mudler/Qwen3.5-35B-A3B-APEX-GGUF@Qwen3.5-35B-A3B-APEX-Balanced.gguf
```

To quantize your own MoE model, the recipes are shell scripts over stock llama.cpp:

```bash
git clone https://github.com/mudler/apex-quant.git
cd apex-quant
./scripts/quantize.sh --i-quality model-f16.gguf model-apex-i-quality.gguf
```

The default configuration assumes 40 transformer layers, as in Qwen3.5-35B-A3B. Set `NUM_LAYERS` for a different depth and the edge and middle boundaries move with it. The full methodology, every experiment and all the plots are in the [technical report](https://github.com/localai-org/apex-quant).
