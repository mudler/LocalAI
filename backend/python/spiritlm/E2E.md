# SpiritLM E2E tests

End-to-end tests for the SpiritLM backend live in `core/http/app_test.go` under the context **SpiritLM backend e2e** (label: `spiritlm`).

## How to run

From the repo root:

```bash
make test-spiritlm
```

This sets `BACKENDS_PATH=./backend/python` and `TEST_DIR=./test-dir`, runs `prepare-test`, then runs Ginkgo with `--label-filter="spiritlm"`.

For the **transcription** test you need `test-dir/audio.wav` (e.g. run `make test-models/testmodel.ggml` once to download it, or set `TEST_DIR` to a directory that contains `audio.wav`).

## Backend setup

1. **Protos**  
   Generate Python gRPC stubs (required for the backend to start):
   ```bash
   cd backend/python/spiritlm && bash protogen.sh
   ```
   Or run the full install (which also creates the venv and installs deps):
   ```bash
   make -C backend/python/spiritlm
   ```

2. **Full e2e pass (all 3 specs pass)**  
   - Install the backend: `make -C backend/python/spiritlm`
   - Download the Spirit LM model from [Meta AI Spirit LM](https://ai.meta.com/resources/models-and-libraries/spirit-lm-downloads/) and place it so the checkpoint directory layout is:
     ```
     <SPIRITLM_CHECKPOINTS_DIR>/
       spiritlm_model/
         spirit-lm-base-7b/   # model files (config.json, tokenizer, etc.)
     ```
   - Run the tests with the checkpoint dir set:
     ```bash
     SPIRITLM_CHECKPOINTS_DIR=/path/to/checkpoints make test-spiritlm
     ```
   - Ensure LocalAI runs the backend with that env (e.g. export it before `make test-spiritlm`, or configure the backend to pass it through).

Without the model, the backend starts and responds to Health, but LoadModel fails; the e2e specs **skip** with a message pointing here, and the suite still **passes** (0 failed).

## Requirements

- Linux (tests skip on other OS)
- SpiritLM backend runnable: `backend/python/spiritlm/run.sh` must exist (satisfied in-tree)
- For backend to start: Python protos generated (`backend_pb2.py`, `backend_pb2_grpc.py`) and venv with grpc/spiritlm (via `make -C backend/python/spiritlm`)
- For all 3 specs to pass: Spirit LM model under `SPIRITLM_CHECKPOINTS_DIR` as above

Tests are skipped if `BACKENDS_PATH` is unset or `BACKENDS_PATH/spiritlm/run.sh` is missing.
