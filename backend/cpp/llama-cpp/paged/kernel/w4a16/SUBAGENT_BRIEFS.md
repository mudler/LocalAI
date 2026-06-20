# W4A16 kernel - subagent dispatch briefs (P3, P4, P5)

**Dispatch strategy.** Each phase = one fresh **Opus-4.8** subagent handed a complete zero-context brief.
Phases are **sequential** (P3 needs P2's correct kernel; P4 needs P3's pipeline; P5 needs P4's tuned kernel),
so dispatch phase N+1 only after phase N's commit lands, and before dispatching, splice phase N's *actual*
deliverable (final kernel shape, configs, fallback set) into the next brief. P2's brief (already dispatched)
is the template; reuse the COMMON section below verbatim in every dispatch.

---

## COMMON (paste into every phase brief)

- **Kernel dev is on the remote DGX** (GB10, sm_121): `ssh -o ConnectTimeout=25 -o ServerAliveInterval=10 -o ServerAliveCountMax=10 dgx.casa '<cmd>'`. Network is FLAKY (re-poll on drop; nohup jobs survive). `llama-cli` HANGS - never use it. Only `llama-bench` + `test-backend-ops` work.
- Checkout `~/llama.cpp-pr24423`, build `~/llama.cpp-pr24423/build` (sm_121, `-DLLAMA_BUILD_TESTS=ON`). Kernel file `ggml/src/ggml-cuda/marlin-w4a16.cu`. Build auto-GLOBs it; no CMakeLists edits. Hook already in `ggml-cuda.cu`, gated behind env `GGML_CUDA_W4A16`.
- Dense test model: `~/bench/q3-32b-gguf/Qwen3-32B-Q4_K_M.gguf`.
- **Builds run detached + poll** (never blocking foreground): write a `~/pN.sh` that builds `--target test-backend-ops llama-bench`, echoes `RC=$?`, runs the gate, echoes `PN_DONE`; `nohup` it; poll `for i in $(seq 1 90); do grep -q PN_DONE ~/pN.out && break; sleep 20; done; tail ~/pN.out`.
- **GPU hygiene:** check `docker ps | grep local-ai` + `nvidia-smi`; `docker stop` a running localai worker if present (authorized); never pkill native procs; never start model servers.
- **Parity gate (must stay green every step):** `GGML_CUDA_W4A16=1 CUDA_VISIBLE_DEVICES=0 ./build/bin/test-backend-ops test -o MUL_MAT -b CUDA0` = **1103/1103**; and flag-unset stays 1103/1103 (byte-identical). A wrong result is worse than a fallback - return false for any shape you can't do correctly.
- **Perf measurement:** `test-backend-ops perf -o MUL_MAT -b CUDA0` (per-shape GFLOPS; the canonical target is q4_K m=4096 k=14336 **n=512**, baseline **47.1 TFLOPS**, ceiling ~213) + `llama-bench -m <model> -ngl 99 -p 512,2048 -n 0 -ub 2048` (baseline pp512 ~718).
- **LocalAI repo (commit here; you do NOT inherit cwd - `cd` explicitly):** `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention`. Plan: `backend/cpp/llama-cpp/paged/W4A16_MARLIN_KERNEL_PLAN.md`. Source mirror: `backend/cpp/llama-cpp/paged/kernel/w4a16/`. After a phase passes: fetch the final `marlin-w4a16.cu` from the DGX (`ssh ... 'cat ...'`), overwrite the mirror, update the plan (mark the phase DONE with numbers), `git commit -s` (DCO sign-off; user is Ettore Di Giacinto <mudler@localai.io>). **No `Co-Authored-By`. No em-dashes anywhere. Trailer `Assisted-by: Claude:opus-4.8 [Claude Code]`. Do NOT push.**
- Final message = the result (gate ?/1103, the perf delta, blockers + resolutions, commit hash). A precise partial result beats a vague success claim.

---

## P3 brief - the Marlin pipeline (the speedup)

**Goal.** Take P2's correct-but-slow kernel from ~47 toward ~150+ TFLOPS (then ~213) on the q4_K n=512 prefill GEMM, **without ever breaking parity**. This is the Marlin design: the math is the same BF16 mma; the speed comes from feeding the tensor cores without stalling.

**Implement, incrementally (re-run the parity gate after each):**
1. **`cp.async` multi-stage pipeline** - double/triple-buffer global->shared loads of both the Q4 weight tiles and the activation tiles so dequant+mma on stage k overlaps the load of stage k+1. (Study `mma.cuh` + how `mmq.cu`/`mmf.cu` stage shared memory; ggml already uses `cp.async`/`__pipeline_*`.)
2. **Offline weight reshuffle** - repack the Q4 weights once into the mma+pipeline-friendly layout (Marlin's interleave) so loads are coalesced and the mma fragment maps directly. Do this as a load-time transform of src0 (a new prepacked buffer keyed off the tensor) - NOT per-call. Document where the repack lives + its memory cost.
3. **Register-resident activation tiles + Stream-K** split of the M dimension across blocks for the prefill (large-M) case so all SMs stay busy.

**Acceptance.** Parity gate stays **1103/1103** at every commit; `test-backend-ops perf` q4_K n=512 climbs materially above 47 TFLOPS (target >=150) and `llama-bench` pp512 climbs above ~718. Report the TFLOPS + t/s after each of the 3 steps so the contribution of each is visible. If a step regresses parity, revert it and report why.

**Reference.** IST-DASLab/marlin (github), arXiv 2408.11743, vLLM machete. Mirror `mmf.cu`'s BF16 GEMM structure; Marlin = that + Q4 dequant-on-load + the pipeline/reshuffle.

**Splice before dispatch:** P2's final kernel structure (tile sizes, which types/shapes it handles vs falls back, helper functions it defined).

---

## P4 brief - tune to the ceiling

**Goal.** Drive the P3 kernel as close to the ~213 TFLOPS ceiling as empirical tuning allows. **No `ncu` on this box** (no driver perms) - tune by throughput: `test-backend-ops perf` + `llama-bench` + `nsys` (throughput only).

**Do.** Parametrize the kernel (template params / constants) over: tile M/N/K, warps per block, pipeline depth (stages), and occupancy (regs, shared-mem budget). Sweep systematically (a script that rebuilds + benches each config, logs q4_K n=512 TFLOPS + pp512/pp2048 t/s), pick the best, hard-set it (with a short comment on the sweep). Check both prefill shapes (n=512 and n=2048) and confirm decode (n=1) didn't regress (it should still route to mat-vec, not this kernel - verify the gating).

**Acceptance.** Best config maximizes q4_K n=512 TFLOPS (stretch ~150-213) with parity **1103/1103** intact; the sweep table (config -> TFLOPS/t-s) is recorded in the plan's P4 section. Report the chosen config + the final pp512/pp2048 t/s vs the 718/750 baseline and vs vLLM's ~3300 single-stream target.

**Splice before dispatch:** P3's pipeline structure + the perf it reached + which knobs are already fixed vs free.

---

## P5 brief - enable + package + (maybe) upstream

**Goal.** Make W4A16 the default dense-Q4 path on Blackwell and ship it through LocalAI.

**Do.**
1. **Flip the gate:** default-ON for sm_120/121 + Q4_0/Q4_K dense when faster, keep an opt-out env (e.g. `GGML_CUDA_W4A16=0`) as an escape hatch. The existing return-false-on-unhandled-shape path is the correctness safety net; keep it. Verify the default (no env) build now runs W4A16 for dense Q4, gate green, faster than the old MMQ baseline.
2. **Package as a LocalAI llama.cpp patch:** produce `backend/cpp/llama-cpp/paged/patches/kernel/0002-w4a16-marlin.patch` (the new files + the `ggml-cuda.cu` hook + the gate flip) that applies cleanly to the pinned llama.cpp, mirroring the existing `patches/kernel/0001-fp4-grouped-moe-scaffold.patch`. Confirm LocalAI's `make backends/llama-cpp` build path can consume it (read `.agents/llama-cpp-backend.md` + the build memory: `make -C backend/cpp/llama-cpp clean` before rebuilds).
3. **Docs:** update `BLACKWELL_KERNEL_GAPS.md` + the plan with the shipped result; add a short note to the LocalAI docs if there's a Blackwell/performance page.
4. **Upstream decision (do NOT open without surfacing first):** ggml has no Marlin-equivalent (issue #1519) so this is net-new upstream value. Draft (do not submit) an upstream PR description + note the sm_121 build-flag caveats; report it for the user to decide.

**Acceptance.** Default Blackwell build uses W4A16 for dense Q4, parity 1103/1103, measurably faster than MMQ; the patch applies + the LocalAI llama-cpp backend builds with it (verify or, if the full backend build is too heavy, document the exact build command + that the patch applies cleanly). Report the end-to-end LocalAI dense-Q4 prefill number vs the start-of-project 765 t/s.

**Splice before dispatch:** P4's final kernel + config + the measured ceiling reached; the exact enable condition decided.
