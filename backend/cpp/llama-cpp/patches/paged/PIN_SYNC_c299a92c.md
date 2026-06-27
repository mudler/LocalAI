# Pin-sync: paged patch-stack -> llama.cpp c299a92c

Status: COMPLETE. The shipped source-only paged patch series (`0001`-`0030`,
28 `.patch` files) was advanced from llama.cpp `9d5d882d` to `c299a92c`
("binaries : Improve rpc-server and export-graph-ops names. (#25045)"),
GPU-rebuilt clean (CUDA sm_121 / GB10), and the bit-exact gate is GREEN on every
path (dense + MoE, paged + non-paged) plus `test-backend-ops`. The 23-commit
upstream jump `9d5d882d..c299a92c` did NOT change our decode output.

## Upstream jump

- OLD LocalAI paged pin: `9d5d882d8cd0f0a9283d87ed5e6fe3ee0d925fb1`
  ("model : Add label for LFM2.5-230M (#25008)")
- NEW LocalAI paged pin: `c299a92c38b6de6a1139617652b66081828648db`
  ("binaries : Improve rpc-server and export-graph-ops names. (#25045)")
- Upstream jump `9d5d882d..c299a92c` = **23 commits**.

## Re-export decision: NONE NEEDED - the source-only series applies STRICT-CLEAN at c299a92c

Unlike the `9d5d882d` sync (which needed 4 patch re-exports), this bump required
**zero patch changes**. The already-shipped source-only series (the result of the
`7e1832b8` strip that removed all stray dev-doc hunks) applies to a fresh clean
`ggml-org/llama.cpp` checkout at `c299a92c` with the build's own **strict
`git apply`** (the `llama.cpp` target in `backend/cpp/llama-cpp/Makefile`:
`git apply --verbose "$p" || exit 1`) and reaches **exit 0** - every one of the
28 patches reported "Applied patch ... cleanly", the sentinel
`src/paged-kv-manager.cpp` was created, and there are **zero** stray
`*_RESULTS.md` / `*_PROGRESS.md` in the resulting tree (source-only invariant
intact). git apply tolerates `@@` line-number offsets, which absorbed the
upstream drift; no hunk context broke.

Therefore the shipped `.patch` files are kept **byte-identical** (no churn). The
patch tarball used for the verification has
`sha256(cat 0*.patch | sort -V) = a99cc1fe4b66a7d0f4adcf9786bf2f9cda40792d7a6a01f36c4619369509114c`.

## Clean build

Fresh clone `~/llama-paged-c299/llama.cpp` @ `c299a92c` (NOT the dev tree), the
28 patches applied as working-tree changes, then:

```
cmake -B build-cuda -DGGML_CUDA=ON -DCMAKE_CUDA_COMPILER=/usr/local/cuda/bin/nvcc \
  -DCMAKE_CUDA_ARCHITECTURES=121 -DGGML_CUDA_NCCL=ON -DGGML_CUDA_FA=ON \
  -DGGML_CUDA_GRAPHS=ON -DGGML_CCACHE=ON -DLLAMA_CURL=OFF -DCMAKE_BUILD_TYPE=Release
cmake --build build-cuda --target llama-completion test-backend-ops -j20
```

Result: configure exit 0 (ggml 0.15.3, commit `c299a92-dirty`), build exit 0,
`build-cuda/bin/llama-completion` + `build-cuda/bin/test-backend-ops` produced.

## GATE: ALL GREEN

Gate command (locked - reproduces the dense baseline byte-for-byte on the OLD
`9d5d882d` build too):
```
llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France is" \
                 -n 48 --temp 0 --seed 1 </dev/null 2>/dev/null | md5sum
# paged dense: prefix  LLAMA_KV_PAGED=1
# paged MoE:   prefix  LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1
```

(a) greedy md5 - all four paths PASS:
| path | model | md5 @ c299a92c | baseline | verdict |
|------|-------|----------------|----------|---------|
| non-paged | dense `q36-27b-nvfp4`   | `5951a5b4d624ce891e22ab5fca9bc439` | `5951a5b4d624ce891e22ab5fca9bc439` | PASS |
| non-paged | MoE `q36-35b-a3b-nvfp4` | `07db32c2bcb78d17a43ed18bc22705cd` | `07db32c2bcb78d17a43ed18bc22705cd` | PASS |
| paged     | dense `q36-27b-nvfp4`   | `5951a5b4d624ce891e22ab5fca9bc439` | `5951a5b4d624ce891e22ab5fca9bc439` | PASS |
| paged     | MoE `q36-35b-a3b-nvfp4` | `8cb0ce23777bf55f92f63d0292c756b0` | `8cb0ce23777bf55f92f63d0292c756b0` (PAGED_BITEXACT_NOTE) | PASS |

(b) `test-backend-ops` (Backend CUDA0) - all PASS:
| op | result |
|----|--------|
| SSM_CONV            | 45/45 OK |
| SSM_CONV_UPDATE     | 16/16 OK |
| SSM_CONV_UPDATE_IDS | 16/16 OK |
| GATED_DELTA_NET     | 84/84 OK |
| MUL_MAT             | 1146/1146 OK |
| MUL_MAT_ID          | 806/806 OK |

(GATED_DELTA_NET grew 36/36 -> 84/84 vs the `9d5d882d` sync because the shipped
series now carries patches `0026`/`0028`'s added per-head/gather test cases; all
pass. SSM_CONV/MUL_MAT/MUL_MAT_ID counts match the prior sync exactly.)

Bit-exactness preserved across the 23-commit upstream jump.

## Canary

`.github/workflows/llama-cpp-paged-canary.yml` and
`.github/scripts/paged-canary-apply.sh` now reference this doc. Because the
series is source-only and applies strict-clean with no `--exclude`, the canary's
`SSM_DECODE_FIX_RESULTS.md` workaround is now inert (the glob matches nothing in
the shipped series) and may be removed on a future canary touch; left in place
here to keep the pin-bump diff minimal.

## Source of truth

The shipped `.patch` files under `backend/cpp/llama-cpp/patches/paged/` are the
source of truth and are unchanged by this bump. The DGX dev tree
(`~/llama-paged-dev`, branch `paged`) was advanced to `c299a92c` for consistency;
the pre-bump state is retained at `paged-prebump-9d5d882d-backup`.
