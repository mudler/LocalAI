# bonsai fork skew patches

The `bonsai` backend reuses `backend/cpp/llama-cpp/grpc-server.cpp` (written against
LocalAI's pinned *upstream* llama.cpp) but compiles it against the PrismML `prism` fork,
which branched from upstream some commits earlier. Any upstream API change that the shared
gRPC server depends on, but that the fork does not yet carry, is back-ported here as a
`*.patch` file and applied to the cloned fork checkout by `../apply-patches.sh`.

Rules:

- One upstream commit (or minimal hunk) per patch, named `NNNN-short-description.patch`.
- Patches are applied with `git apply` from the fork's checkout root.
- `apply-patches.sh` fails fast if a patch stops applying cleanly — that is the signal the
  fork has caught up (or diverged), so re-cut or drop the patch.
- Keep this set as small as possible; the long-term fix is the fork rebasing onto a newer
  upstream (or Q1_0/Q2_0 landing in mainline llama.cpp, retiring this backend entirely).
