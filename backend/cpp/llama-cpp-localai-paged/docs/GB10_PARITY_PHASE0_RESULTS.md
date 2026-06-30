# GB10 Parity Phase 0 Results

Status: in progress.

## Preflight

- DGX host: `promaxgb10-4ad8`
- Docker containers: `none`
- GPU compute apps: `none`
- GPU lock owner: `FREE released-by-claude-fp4norm-profile 1782828229`
- LocalAI worktree SHA: `d288a0300f36f7c126d62d997809bb03f297a3ac`
- Local llama.cpp fork SHA: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- DGX artifact directory: `~/bench/reopen_phase0`

## Baseline Runs

No baseline runs have been started yet.

## Source Provenance

- Local llama.cpp fork: `/home/mudler/_git/llama.cpp`
- Branch: `localai-paged`
- Working tree: clean
- HEAD: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- Base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- Merge-base with base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- LocalAI patch count: `38`
- LocalAI patch mirror: applies cleanly to the base pin and tree-matches fork
  HEAD.
- Tree hash after patch application: `a73d759350277532a14e853e1fe78f08bbb74ce8`

## Open Items

- Reproduce paged prefill and decode baselines.
- Find or recreate vLLM graph-node-traced difference-method decode artifacts.
