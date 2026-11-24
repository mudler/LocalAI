+++
disableToc = false
title = "Engine runtime settings"
weight = 95
description = "Runtime settings of an inference engine on a model: how LocalAI maps them to YAML keys, how an engine registers, how to add one, and the llama.cpp settings."
keywords = ["engine settings", "runtime tuning", "llama.cpp", "llama-server", "options", "ngl", "flash attention", "kv cache", "override tensor", "moe"]
+++

An inference engine has its own runtime settings: the context size, the GPU
offload, the KV cache type, and many more. LocalAI keeps them in the model's
YAML file, as YAML keys or as entries of the `options` list. The model editor
has a **Runtime tuning** section that shows the settings of the model's engine
as fields, so you do not have to know which key or option holds each one.

This page explains how the settings are described, how an engine registers,
and how to add an engine. Then it lists the settings of llama.cpp, the first
engine.

## How it works

Each engine is one description in Go, in the package `pkg/enginesettings`. The
description is the only place that knows the settings of the engine. The model
editor, the API and the LocalAI Assistant read it, so they always agree.

An engine description has these parts:

| Part | What it is |
|---|---|
| ID and name | A stable id, for example `llama-cpp`, used in the API routes, and a display name, for example `llama.cpp`. |
| Backends | The model backend names the engine serves: exact names, families (a name and its hardware builds, such as `cuda12-llama-cpp`), names to exclude, and whether a model with no backend uses this engine. |
| Flag table | One row per setting: a key, where the value goes (a YAML key or an `options` entry), the option name and its aliases, the command-line spellings, the type, the allowed values, the limits, the default, a doc line and the editor group. |
| Groups | The editor sections, in the order the editor draws them. |
| Combined controls | Optional. One control that writes several settings, such as a load mode that writes `mmap`, `mmlock` and `direct_io`. |
| Presets | Optional. Whole-setup starting points. |
| Capabilities | Optional features. `command-line`: the engine reads a pasted command and writes the command of a config. `tensor-placement`: the engine builds and reads an `options` entry that keeps MoE expert tensors on the CPU. |

The editor draws the fields, groups, combined controls and presets from the
schema of the engine. It shows the command preview and the paste box only for
an engine with `command-line`, and the placement control only for an engine
with `tensor-placement`. The editor has no code for a specific engine.

The editor finds the engine of a model from its `backend` key, with
`GET /api/engines?backend=<name>`. A model whose backend no engine serves has
no **Runtime tuning** section. You can still set every option in the YAML tab.

## Add an engine

To add the runtime settings of another engine:

1. Create a package under `pkg/enginesettings/`, for example
   `pkg/enginesettings/myengine`, with a function `Engine()` that returns an
   `*enginesettings.Engine`.
2. Set the id, the display name and the `Backends` match. Use the backend names
   of the gallery.
3. Write the flag table. Each row needs a unique key, a target (`config` with a
   YAML path, or `option` with an option name), a type, a doc line and a group
   from the `Groups` list. Add the command-line spellings if the engine has a
   command line.
4. Optional: add combined controls, presets and `ReservedOptions` (option names
   that another part of LocalAI writes).
5. Optional: set `Command` for the `command-line` capability. Give the binary
   name, the flags that LocalAI sets itself (`Ignored`), and handlers for flags
   that do not map to one key (`Special`).
6. Optional: set `Placement` for the `tensor-placement` capability.
7. Register the engine in `pkg/enginesettings/builtin`. The registry validates
   the description at start-up, so a broken table fails early.
8. Write Ginkgo tests for the table: parse and build round trips, the backend
   match, and the presets.
9. Add translations for new group, field and preset labels under
   `engineSettings` in the `modelEditor` namespace of the React UI. Without a
   translation, the editor shows the name or the English text of the schema.
10. Document the settings in a section of this page.

Other engines, such as vLLM, can be added this way. Today only llama.cpp is
registered.

## llama.cpp

The engine id is `llama-cpp`. It serves the `llama-cpp` backend, every
hardware build of it, and models with no backend. It has the `command-line`
and `tensor-placement` capabilities. A model that uses it runs `llama-server`
settings that you write in its YAML file.

### Where each flag goes

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

#### Load mode

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

### Example

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

### The Runtime tuning section

Open a model in the model editor (**Models**, then **Edit** on the model). When
the model's backend is `llama-cpp` (or a build of it, or no backend at all),
the **Interactive** tab has a **Runtime tuning** section with the badge
`llama.cpp`.

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

### Paste a command

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

### Tensor placement

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

These routes need an administrator. The LocalAI Assistant has the same
operations as the `parse_engine_command` and `preview_engine_command` tools.

| Route | Purpose |
|---|---|
| `GET /api/engines` | The registered engines: id, name, binary, the backend names each serves, and the capabilities. With `?backend=<name>`, only the engine that serves that backend. An empty name is a model with no backend. |
| `GET /api/engines/{engine}/settings-schema` | The schema of one engine: the flag table (key, target, YAML path, option name and aliases, CLI spellings, type, allowed values, limits, default, doc line, group), the groups, the combined controls, the presets, the reserved options and the capabilities. |
| `POST /api/engines/{engine}/parse-command` | Needs `command-line`. Body `{"command": "..."}`. Returns `fields`, `options`, `unmapped` and `warnings`, plus `placements` for an engine with `tensor-placement`. |
| `POST /api/engines/{engine}/command-preview` | Needs `command-line`. Body `{"config": {...}}` with model YAML keys. Returns `{"command": "...", "warnings": []}`. |
| `POST /api/engines/{engine}/placement` | Needs `tensor-placement`. Body `{"placement": {...}, "layers": "0-6,18-23"}` returns the `options` entry. Body `{"option": "-ot:..."}` returns its structured form, or `raw: true`. |

An unknown engine returns status 404. An operation of a capability that the
engine does not have returns status 400.

```bash
curl -s http://localhost:8080/api/engines/llama-cpp/parse-command \
  -H "Authorization: Bearer $LOCALAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"command": "llama-server -m model.gguf -c 8192 -ngl 99 -fa on"}'
```

A command longer than 16 KiB is refused with status 413. A command with an
unclosed quote is refused with status 400.
