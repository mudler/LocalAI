# EXL3 gallery entries

## Goal

Add four gallery entries that expose the EXL3 configurations validated or
tracked by `vllm.cpp`. Pin each Hugging Face artifact to the revision recorded
by its source or benchmark evidence.

## Entries

### Qwen3.8 target

Add `qwen3.8-27b-exl3-vllm-cpp` for
`Mia-AiLab/Qwen3.8-27B-EXL3-3.5bpw`. This entry serves the target without a
draft model.

Use revision `19441ac874c4018295da848e250f23511361cda4`. Configure an 8,192-token
context, 2,048 cache blocks, eight sequences, and 16,384 batched tokens. Disable
prefix caching to match the measured serving configuration.

### Qwen3.8 with DFlash2

Add `qwen3.8-27b-dflash2-exl3-vllm-cpp`. This entry stages the Qwen3.8 target
and `Mia-AiLab/Qwen3.8-27B-DFlash2-EXL3-5.0bpw` at revision
`4f0436269bca761b071f05319e8e04a87cc633f9`.

Configure the `dflash` method with seven speculative tokens. Use the shipped
paged draft route. Apply the same serving limits as the target-only entry.

Tag this entry with `dflash` because it enables speculative decoding. Declare
the target-only entry as its variant. LocalAI can then prefer the faster entry
when the host supports it.

### DeepSeek V4 Flash for Spark

Add `deepseek-v4-flash-spark-exl3-vllm-cpp` for
`0xSero/deepseek-v4-flash-0731-spark`. Use the current repository revision,
`ce5ff0f1efb2e184aafc759d281bfae47d3a359c`. State that the `vllm.cpp`
runtime record used the older revision `22f28d32b9b29b4352eaa380ff8c2c170b2847ab`.

Describe the entry as a Spark and GB10-oriented REAP-K216 checkpoint. State its
large memory requirement and CUDA requirement. Do not claim a completed speed
or correctness gate that the source record does not contain.

### DeepSeek V4 Flash 3.0 bpw

Add `deepseek-v4-flash-exl3-3bpw-vllm-cpp` for
`0xSero/DeepSeek-V4-Flash-0731-EXL3-3.0bpw`. Use the current repository
revision, `e0bf84ac76a5100e8790c22ad10b70b1e2d06d71`.

Tag and describe this entry as experimental. The model card states that the
artifact is structurally complete, but end-to-end generation has not passed.
Keep this entry separate from the Spark entry because the repositories use
different layouts and have different runtime evidence.

## Artifact staging

Use LocalAI's Hugging Face artifact source for each repository. Stage complete
model repositories because these safetensors checkpoints need configuration,
tokenizer, index, and weight files.

Assign the Qwen draft artifact to a companion target. Pass its staged path in
the `vllm-cpp` speculative configuration. Do not download files through backend
startup logic.

## User-visible metadata

Use the `vllm-cpp`, `exl3`, `gpu`, and `cuda` tags on all four entries. Add
architecture, reasoning, tool-calling, and speculative-decoding tags only when
the configured model supports them.

Descriptions must distinguish measured results from unresolved work. The Qwen
DFlash2 description can cite the measured configuration and throughput. The
DeepSeek descriptions must not imply an end-to-end validation that does not
exist.

## Validation

Run the gallery schema and focused gallery tests. Add a focused test if the
artifact or variant structure is not already covered.

Validate these properties:

- Every name is unique.
- Every variant points to an existing entry.
- Each Hugging Face source has a pinned revision.
- The DFlash2 entry stages both repositories and passes the draft path.
- Only the configured DFlash2 entry has the `dflash` tag.
- YAML parsing and gallery loading succeed.

No model download or GPU benchmark is part of this LocalAI change. The
`vllm.cpp` evidence supplies the runtime record.

## Out of scope

- Changes to the `vllm-cpp` backend binaries.
- New EXL3 kernels or model loaders.
- New benchmark claims.
- Gallery entries for unselected EXL3 bit widths.
