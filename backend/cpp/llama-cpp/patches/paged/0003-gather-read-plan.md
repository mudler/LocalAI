# Patch 0003 — paged gather-read: exact implementation plan

**Goal:** a sequence attends only its own (compacted) cells via `ggml_get_rows`, instead of the scattered
`[0,n_kv)` window. Token-identical (attention is permutation-invariant over the KV set). **Gated**: stock
path stays byte-identical (no new ops unless `LLAMA_KV_PAGED`).

**Base:** applies on top of 0001+0002 at the pin. Dev tree: `backend/cpp/llama-cpp-paged-dev` (branch `paged`).

## Design

The gather is keyed off one runtime index list (the sequence's used cells, in a fixed order), exposed as a
graph input (mirroring `k_idxs`). In `build_attn`, gather K, V **and the kq_mask** by that same index, so all
three stay aligned. `n_gathered` replaces `n_kv` for the attention. Only active when the cache is in paged
mode (a new `is_paged()` flag set when `LLAMA_KV_PAGED`/find_slot used permuted placement).

ggml note: `ggml_get_rows(a,b)` gathers `a`'s **ne1** by `b` (I32). Raw K is `[n_embd_k_gqa, kv_size, n_stream]`
→ ne1 = cells → direct. The mask is `[n_kv, n_tokens, 1, n_stream]` → n_kv is **ne0**, so gather as
`transpose → get_rows → transpose`.

### KEY CORRECTIONS (found while implementing — these change the edits)

1. **Gather index = ALL used (non-empty) cells in `[0,n_kv)`, NOT `sinfo.idxs`.** `sinfo.idxs` is only the
   *current ubatch's write slots*; attention reads the *full history*. The query set per token is masked by
   `kq_mask`, so gathering the union of all used cells + gathering the mask the same way is token-identical
   and drops exactly the empty (already-masked) cells. So: `gather = { i in [0,n_kv) : !cells.is_empty(i) }`.

2. **Static-graph size is fine because llama.cpp rebuilds the graph every ubatch.** `n_gather` (used-cell
   count) is therefore a build-time constant for that ubatch — `build_input_gather_idxs` sizes the I32
   tensor to `get_n_gather()` computed at build, `set_input_gather_idxs` fills the identical cell list. They
   MUST use the same loop (`for i in [0,n_kv): if !is_empty(i) push i`) so build-order == fill-order.

3. **K/V gather can live entirely in `build_attn`, no cache get_k change.** The `get_k` 4d view is contiguous
   in `[ne0,ne1,ne2]` from cell 0 (nb2 == n_embd_head*n_head_kv*elemsz), so for **single stream (ns==1)**:
   `reshape_3d(k, n_embd_head*n_head_kv, n_kv, 1) → get_rows(., gi) → reshape_4d(., n_embd_head, n_head_kv, n_gather, 1)`.
   Multi-stream (ns>1) breaks contiguity (nb3 uses kv_size) → gate to ns==1 first, multi-stream follow-up.

4. So the ONLY cache additions are `is_paged()`, `get_n_gather(n_kv)`, `build/set_input_gather_idxs(n_kv)`;
   everything else (K/V/mask gather) is in `build_attn`. `set_input_kq_mask` is **unchanged** (built over
   n_kv, then gathered). Smaller than the 7-edit estimate above.

## Edits

### 1. `src/llama-kv-cache.h` — declare gather infra (in `llama_kv_cache`)
```cpp
    bool        is_paged() const { return paged_active; }            // near get_size()
    ggml_tensor * build_input_gather_idxs(ggml_context * ctx, const slot_info & sinfo) const;
    void          set_input_gather_idxs (ggml_tensor * dst, const slot_info & sinfo) const;
    uint32_t      get_n_gather(const slot_info & sinfo) const;       // == sum of used cells gathered
```
Add member `mutable bool paged_active = false;` and in `llama_kv_cache_context` forward the three (like
`build_input_k_idxs`/`get_n_kv`).

### 2. `src/llama-kv-cache.cpp`
- In `find_slot`, in the paged branch (0002), set `paged_active = true;` on success.
- `get_n_gather(sinfo)` = `sinfo.idxs[0].size()` summed over streams (the count actually placed).
- `build_input_gather_idxs`: `ggml_new_tensor_1d(ctx, GGML_TYPE_I32, get_n_gather(sinfo)); ggml_set_input(...)`.
- `set_input_gather_idxs`: fill `data[k++] = strm_off + sinfo.idxs[s][i]` for every placed cell (same order
  the mask/k/v will see). This is the canonical gather order.

### 3. `src/llama-graph.h` — `llm_graph_input_attn_kv`
Add `ggml_tensor * gather_idxs = nullptr;` + `ggml_tensor * get_gather_idxs() const { return gather_idxs; }`.

### 4. `src/llama-graph.cpp`
- `llm_graph_input_attn_kv::set_input`: if `mctx->is_paged()` → `mctx->set_input_gather_idxs(gather_idxs, ...)`.
- `build_attn_inp_kv` (creates the input): if `mctx_cur->is_paged()` → `inp->gather_idxs =
  mctx_cur->build_input_gather_idxs(ctx0, ...)`.
- `build_attn` (the kv overload, ~2356): after `k`,`v`,`kq_mask`:
```cpp
if (ggml_tensor * gi = inp->get_gather_idxs()) {
    k = ggml_get_rows(ctx0, k, gi);                                   // [d, n_gather, ...] (reshape view ok)
    v = v_trans ? /* gather columns */ : ggml_get_rows(ctx0, v, gi);
    ggml_tensor * m = ggml_cont(ctx0, ggml_transpose(ctx0, kq_mask)); // [n_tokens, n_kv]
    m = ggml_get_rows(ctx0, m, gi);                                   // [n_tokens, n_gather]
    kq_mask = ggml_cont(ctx0, ggml_transpose(ctx0, m));              // [n_gather, n_tokens]
}
ggml_tensor * cur = build_attn_mha(q, k, v, kq_b, kq_mask, sinks, v_mla, kq_scale, il);
```
Note: `get_k` returns the reshaped 4d view; gather must run on a cell-major shape. Simplest: add a paged
variant `get_k(ctx,il)` that returns `ggml_get_rows` of the **raw** `layers[ikv].k` then reshapes to
`[n_embd_head, n_head_kv, n_gather, ns]`. Do the gather in the cache, not the graph, for K/V; keep only the
mask gather in the graph. (Cleaner — revisit during impl.)

### 5. V-transposed path
When `!flash_attn`, V is stored transposed `[kv_size, n_embd_v_gqa]`; gather its **rows** (ne1 = n_embd) won't
work — gather columns via the same idx on the non-transposed store, OR force `is_paged()` to require
flash-attn for the first cut (`GGML_ASSERT`) and handle v_trans in a follow-up.

## Verification (the gate)
```sh
cmake --build build-cpu --target llama-simple -j
M=Qwen3-0.6B.Q4_K_M.gguf ; P="<the 0002 prompt>"
build-cpu/bin/llama-simple -m $M -n 64 "$P" > a.txt                    # stock
LLAMA_KV_PAGED=1 build-cpu/bin/llama-simple -m $M -n 64 "$P" > b.txt   # paged gather-read
diff a.txt b.txt        # MUST be identical
```
Also assert (debug) that `n_gather < n_kv` on a multi-chunk sequence (proves compaction, not identity).
Export only when identical: `git format-patch HEAD~1 -o patches/ --start-number 3 -N`.

## Risks
- Mask transpose/layout: if `b.txt` diverges, dump the gathered mask vs expected for token 0; off-by-order
  means the `set_input_gather_idxs` order ≠ the get_k gather order — they MUST use the identical loop.
- flash-attn vs not: do flash-attn first (simpler mask), then v_trans.
