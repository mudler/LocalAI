+++
disableToc = false
title = "Fine-Tuning"
weight = 18
url = '/features/fine-tuning/'
+++

LocalAI supports fine-tuning LLMs directly through the API and Web UI. Fine-tuning is powered by pluggable backends that implement a generic gRPC interface, allowing support for different training frameworks and model types.

## Supported Backends

| Backend | Domain | GPU Required | Training Methods | Adapter Types |
|---------|--------|-------------|-----------------|---------------|
| **unsloth** | LLM fine-tuning | Yes (CUDA) | SFT, GRPO | LoRA/QLoRA |
| **trl** | LLM fine-tuning | No (CPU or GPU) | SFT, DPO, GRPO, RLOO, Reward, KTO, ORPO | LoRA, Full |

## Enabling Fine-Tuning

Fine-tuning is disabled by default. Enable it with:

```bash
LOCALAI_ENABLE_FINETUNING=true local-ai
```

When authentication is enabled, fine-tuning is a per-user feature (default OFF). Admins can enable it for specific users via the user management API.

## Quick Start

### 1. Start a fine-tuning job

```bash
curl -X POST http://localhost:8080/api/fine-tuning/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "model": "unsloth/tinyllama-bnb-4bit",
    "backend": "unsloth",
    "training_method": "sft",
    "training_type": "lora",
    "dataset_source": "yahma/alpaca-cleaned",
    "num_epochs": 1,
    "batch_size": 2,
    "learning_rate": 0.0002,
    "adapter_rank": 16,
    "adapter_alpha": 16,
    "extra_options": {
      "max_seq_length": "2048",
      "load_in_4bit": "true"
    }
  }'
```

### 2. Monitor progress (SSE stream)

```bash
curl -N http://localhost:8080/api/fine-tuning/jobs/{job_id}/progress
```

### 3. List checkpoints

```bash
curl http://localhost:8080/api/fine-tuning/jobs/{job_id}/checkpoints
```

### 4. Export model

```bash
curl -X POST http://localhost:8080/api/fine-tuning/jobs/{job_id}/export \
  -H "Content-Type: application/json" \
  -d '{
    "export_format": "gguf",
    "quantization_method": "q4_k_m",
    "output_path": "/models/my-finetuned-model"
  }'
```

## API Reference

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/fine-tuning/jobs` | Start a fine-tuning job |
| `GET` | `/api/fine-tuning/jobs` | List all jobs |
| `GET` | `/api/fine-tuning/jobs/:id` | Get job details |
| `DELETE` | `/api/fine-tuning/jobs/:id` | Stop a running job |
| `GET` | `/api/fine-tuning/jobs/:id/progress` | SSE progress stream |
| `GET` | `/api/fine-tuning/jobs/:id/checkpoints` | List checkpoints |
| `POST` | `/api/fine-tuning/jobs/:id/export` | Export model |
| `POST` | `/api/fine-tuning/datasets` | Upload dataset file |

### Job Request Fields

| Field | Type | Description |
|-------|------|-------------|
| `model` | string | HuggingFace model ID or local path (required) |
| `backend` | string | Backend name: `unsloth` or `trl` (default: `trl`) |
| `training_method` | string | `sft`, `dpo`, `grpo`, `rloo`, `reward`, `kto`, `orpo` |
| `training_type` | string | `lora` or `full` |
| `dataset_source` | string | HuggingFace dataset ID or local file path (required) |
| `adapter_rank` | int | LoRA rank (default: 16) |
| `adapter_alpha` | int | LoRA alpha (default: 16) |
| `num_epochs` | int | Number of training epochs (default: 3) |
| `batch_size` | int | Per-device batch size (default: 2) |
| `learning_rate` | float | Learning rate (default: 2e-4) |
| `gradient_accumulation_steps` | int | Gradient accumulation (default: 4) |
| `warmup_steps` | int | Warmup steps (default: 5) |
| `optimizer` | string | `adamw_torch`, `adamw_8bit`, `sgd`, `adafactor`, `prodigy` |
| `extra_options` | map | Backend-specific options (see below) |

### Backend-Specific Options (`extra_options`)

#### Unsloth

| Key | Description | Default |
|-----|-------------|---------|
| `max_seq_length` | Maximum sequence length | `2048` |
| `load_in_4bit` | Load model in 4-bit quantization | `true` |
| `packing` | Enable sequence packing | `false` |
| `use_rslora` | Use Rank-Stabilized LoRA | `false` |

#### TRL

| Key | Description | Default |
|-----|-------------|---------|
| `max_seq_length` | Maximum sequence length | `512` |
| `packing` | Enable sequence packing | `false` |
| `trust_remote_code` | Trust remote code in model | `false` |
| `load_in_4bit` | Enable 4-bit quantization (GPU only) | `false` |

#### DPO-specific (training_method=dpo)

| Key | Description | Default |
|-----|-------------|---------|
| `beta` | KL penalty coefficient | `0.1` |
| `loss_type` | Loss type: `sigmoid`, `hinge`, `ipo` | `sigmoid` |
| `max_length` | Maximum sequence length | `512` |

#### GRPO-specific (training_method=grpo)

| Key | Description | Default |
|-----|-------------|---------|
| `num_generations` | Number of generations per prompt | `4` |
| `max_completion_length` | Max completion token length | `256` |

### Export Formats

| Format | Description | Notes |
|--------|-------------|-------|
| `lora` | LoRA adapter files | Smallest, requires base model |
| `merged_16bit` | Full model in 16-bit | Large but standalone |
| `merged_4bit` | Full model in 4-bit | Smaller, standalone |
| `gguf` | GGUF format | For llama.cpp, requires `quantization_method` |

### GGUF Quantization Methods

`q4_k_m`, `q5_k_m`, `q8_0`, `f16`, `q4_0`, `q5_0`

## Web UI

When fine-tuning is enabled, a "Fine-Tune" page appears in the sidebar under the Agents section. The UI provides:

1. **Job Configuration** — Select backend, model, training method, adapter type, and hyperparameters
2. **Dataset Upload** — Upload local datasets or reference HuggingFace datasets
3. **Training Monitor** — Real-time loss chart, progress bar, metrics display
4. **Export** — Export trained models in various formats

## Dataset Formats

Datasets should follow standard HuggingFace formats:

- **SFT**: Alpaca format (`instruction`, `input`, `output` fields) or ChatML/ShareGPT
- **DPO**: Preference pairs (`prompt`, `chosen`, `rejected` fields)
- **GRPO**: Prompts with reward signals

Supported file formats: `.json`, `.jsonl`, `.csv`

## Architecture

Fine-tuning uses the same gRPC backend architecture as inference:

1. **Proto layer**: `FineTuneRequest`, `FineTuneProgress` (streaming), `StopFineTune`, `ListCheckpoints`, `ExportModel`
2. **Python backends**: Each backend implements the gRPC interface with its specific training framework
3. **Go service**: Manages job lifecycle, routes API requests to backends
4. **REST API**: HTTP endpoints with SSE progress streaming
5. **React UI**: Configuration form, real-time training monitor, export panel
