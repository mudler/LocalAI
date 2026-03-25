# gen_inference_defaults

This tool fetches per-model-family inference parameter defaults from [unsloth's inference_defaults.json](https://github.com/unslothai/unsloth/blob/main/studio/backend/assets/configs/inference_defaults.json), validates the data, remaps field names to LocalAI conventions, and writes `core/config/inference_defaults.json`.

## What it does

1. Fetches the latest `inference_defaults.json` from unsloth's repo
2. Validates that every entry has required fields (`temperature`, `top_p`, `top_k`)
3. Validates that every pattern references an existing family
4. Warns if pattern ordering would cause shorter prefixes to shadow longer ones
5. Remaps `repetition_penalty` → `repeat_penalty` (LocalAI naming)
6. Filters to allowed fields only: `temperature`, `top_p`, `top_k`, `min_p`, `repeat_penalty`, `presence_penalty`
7. Writes the validated JSON to `core/config/inference_defaults.json`

## Usage

```bash
# Only regenerate if the file is missing (runs during make build)
make generate

# Force re-fetch from unsloth
make generate-force

# Or directly via go generate
go generate ./core/config/...
```

## Automation

The GitHub Actions workflow `.github/workflows/bump-inference-defaults.yml` runs `make generate-force` daily and opens a PR if the upstream data changed.
