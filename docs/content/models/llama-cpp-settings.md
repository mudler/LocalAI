+++
disableToc = false
title = "llama.cpp runtime settings"
weight = 95
description = "Set llama-server options on a llama-cpp model: the YAML keys, the options list, tensor placement, and how to paste a llama-server command."
keywords = ["llama.cpp", "llama-server", "options", "ngl", "flash attention", "kv cache", "override tensor", "moe"]
+++

A model that uses the `llama-cpp` backend runs `llama-server` settings that you
write in its YAML file. The model editor has a **Runtime tuning** section for
them. This page lists the keys, shows how a `llama-server` command maps to them,
and describes the section.

## Where each flag goes

| `llama-server` flag | Model YAML |
|---|---|
| `-m`, `--model` | `parameters.model` |
| `-c`, `--ctx-size` | `context_size` |
| `-ngl`, `--gpu-layers` | `gpu_layers` |
| `-t`, `--threads` | `threads` |
| `-b`, `--batch-size` | `parameters.batch` |
| `-fa`, `--flash-attn on\|off\|auto` | `flash_attention` |
| `-ctk`, `--cache-type-k` | `cache_type_k` |
| `-ctv`, `--cache-type-v` | `cache_type_v` |
| `--mmap`, `--no-mmap`, `--mlock` | `mmap`, `mmlock` |
| `--temp`, `--top-p`, `--top-k`, `--min-p` | `parameters.temperature`, `parameters.top_p`, `parameters.top_k`, `parameters.min_p` |
| `--jinja`, `--no-jinja` | `options: [use_jinja:true]` |
| `-np`, `--parallel` | `options: [parallel:N]` |
| `-ub`, `--ubatch-size` | `options: [ubatch:N]` |
| `-tb`, `--threads-batch` | `options: [threads_batch:N]` |
| `-fit on\|off` | `options: [fit:off]` |
| `--no-op-offload` | `options: [no_op_offload]` |
| `-dio`, `--direct-io` | `options: [direct_io:true]` |
| `-ot`, `--override-tensor` | `options: ['-ot:<pattern>=<buffer>']` |

Any other flag works too. Write it in `options` with a leading dash and put a
colon between the flag and its value, for example `--rope-freq-base:10000`.
LocalAI passes the entry to the `llama-server` parser as it is.

Sampling values go in `parameters`, not in raw flags. A request that sets its
own `temperature`, `top_p`, `top_k` or `min_p` replaces the value of a raw flag.

### Load mode

`--load-mode` is a combination of three settings. LocalAI derives it:

| `--load-mode` | Model YAML |
|---|---|
| `none` | `mmap: false` (with no `mmlock` and no `direct_io`) |
| `mmap` | `mmap: true` |
| `mlock` | `mmap: false` and `mmlock: true` |
| `mmap_mlock` | `mmap: true` and `mmlock: true` |
| `direct_io` | `options: [direct_io:true]` |

`--load-mode auto` has no LocalAI equivalent. The paste flow leaves the keys
unset and shows a warning.

## Example

This command:

```bash
llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --jinja --ctx-size 18000 --parallel 1 \
  --cache-type-k q8_0 --cache-type-v q8_0 --temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 \
  -ub 64 -ngl 99 -fit off -fa on -t 6 -tb 12 --no-op-offload --load-mode none \
  -ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"
```

is this model file:

```yaml
name: inkling-2-mini
backend: llama-cpp
parameters:
  model: Inkling-2-Mini-UD-Q3_K_XL.gguf
  temperature: 1.0
  top_p: 1.0
  top_k: 0
  min_p: 0.0
context_size: 18000
threads: 6
gpu_layers: 99
flash_attention: "on"
cache_type_k: q8_0
cache_type_v: q8_0
mmap: false
options:
  - use_jinja:true
  - parallel:1
  - ubatch:64
  - threads_batch:12
  - fit:off
  - no_op_offload
  - '-ot:blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU'
```

## The Runtime tuning section

Open a model in the model editor (**Models**, then **Edit** on the model). When
the model's backend is `llama-cpp` (or a build of it, or no backend at all),
the **Interactive** tab has a **Runtime tuning** section. A model on another
backend does not show it.

- **Fields.** The settings of the table above, in the groups Memory, Speed,
  Sampling defaults and Chat template. Each field shows its `llama-server` flag
  and the server's description. An empty field, or **Default**, keeps the
  setting out of the file. A value that does not fit the type or the range is
  marked, and **Save** stays off until you correct it.
- **Load mode.** One choice for `mmap`, `mmlock` and `direct_io`, as in the
  load mode table.
- **Tensor placement.** Off by default. See [Tensor placement](#tensor-placement).
- **Start from a setup.** Three presets fill several fields at once: **Big MoE
  on a small GPU** (all layers on the GPU, experts on the CPU, 8-bit KV cache),
  **Max context** (128K context, flash attention, 8-bit KV cache) and **Low
  VRAM** (4K context, 4-bit KV cache, a small micro-batch). The section lists
  each setting it changed, with **Undo**. A setting you edit after the preset
  keeps your value when you undo.
- **Command preview.** The `llama-server` command that matches the form, with a
  **Copy** button. It follows your edits.
- **Advanced options.** The other entries of `options`, one per row. An entry
  that a field above sets is refused here, so no setting is written twice.

The section edits the same file as the YAML tab. Nothing is written until you
press **Save**, and a YAML save shows in the section.

## Paste a command

In the **Runtime tuning** section, paste the command in **Paste a llama-server
command** and press **Fill the form**. LocalAI reads the text and writes what it
found into the fields. The section lists the settings that changed, with
**Undo**.

- The text is only split into words, with the quoting rules of a POSIX shell.
  Nothing is run, and `$(...)`, backticks and `;` are plain text.
- Flags with a first-class key go to that key.
- Flags without one stay in `options` as raw entries. They are also listed as
  unmapped, so you see what was not turned into a field. **Remove** drops one.
- Flags that LocalAI sets itself (`--host`, `--port`, `--api-key`, `-hf` and
  similar) are listed as ignored, with the reason, and are not kept.
- A value that does not fit (for example `-c abc`) is listed as unmapped with the
  reason and is not kept. **Keep as raw option** adds it to `options` as written.
- A model path in `-m` maps to `parameters.model`. LocalAI expects a file
  name inside the models directory, so the editor warns about a path. The editor
  keeps the model's own file and shows the one of the command with **Use this
  file**.

The editor also shows a live preview of the command that matches the form.
The preview is text for you to read. It is never run.

## Tensor placement

Placement is opt-in. LocalAI never adds an `-ot` entry for you, and a model
without one loads as before.

On a model with mixture-of-experts layers (`ffn_*_exps` tensors), you can keep
the large expert tensors in system memory and the rest on the GPU. The
**Tensor placement** group of the editor offers three presets, a **Custom
regex** box for any other pattern, and shows the value it writes:

| Preset | Result |
|---|---|
| Experts on the CPU, all layers | `-ot:\.ffn_.*_exps.*=CPU` |
| Experts on the CPU for some layers, for example `0-6,18-23` | `-ot:blk\.([0-6]\|1[89]\|2[0-3])\.ffn_.*_exps.*=CPU` |
| First N layers fully on the GPU, experts of the rest on the CPU | the pattern matches layer N and higher |

Ranges are sorted and merged, and layers run from 0 to 999. If you give the
number of layers, the last preset writes a range, such as `8-23`. The generated
patterns contain no comma, because `llama-server` splits the value on commas.

When you open a model, the editor shows the picker for a pattern that LocalAI
generated. Any other pattern is shown as raw text.

## API

These routes need an administrator. The same operations are available to the
LocalAI Assistant as the `parse_llama_cpp_command` and
`preview_llama_cpp_command` tools.

| Route | Purpose |
|---|---|
| `GET /api/llama-cpp/settings-schema` | The table of supported flags: key, YAML path, CLI spellings, type, allowed values, limits, default, doc line and group (`memory`, `speed`, `sampling`, `template`, `placement`). |
| `POST /api/llama-cpp/parse-command` | Body `{"command": "..."}`. Returns `fields`, `options`, `unmapped`, `warnings` and `placements`. |
| `POST /api/llama-cpp/command-preview` | Body `{"config": {...}}` with the keys above. Returns `{"command": "...", "warnings": []}`. |
| `POST /api/llama-cpp/tensor-placement` | Body `{"placement": {...}, "layers": "0-6,18-23"}` returns the `-ot` option. Body `{"option": "-ot:..."}` returns its structured form, or `raw: true`. |

```bash
curl -s http://localhost:8080/api/llama-cpp/parse-command \
  -H "Authorization: Bearer $LOCALAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"command": "llama-server -m model.gguf -c 8192 -ngl 99 -fa on"}'
```

A command longer than 16 KiB is refused with status 413. A command with an
unclosed quote is refused with status 400.
