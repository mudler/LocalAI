tinybvh — vendored header
=========================

Upstream:   https://github.com/jbikker/tinybvh
Version:    1.7.1   (README.md "# Version 1.7.1")
License:    MIT — Copyright (c) 2024 Jacco Bikker, see LICENSE
Imported:   2026-08-11
Commit:     <to be pinned — imported from a local source drop, not a clone>

Imported paths (upstream -> here):
    tiny_bvh.h -> tiny_bvh.h
    LICENSE    -> LICENSE

Nothing else: not the demos, not tiny_ocl.h / tiny_scene.h, not testdata/,
not the OpenCL or compute kernels.

The header is unmodified. tiny_bvh_impl.cpp is ours and exists only to define
TINYBVH_IMPLEMENTATION in exactly one translation unit.

Coupling to watch on an upstream bump
-------------------------------------
tinybvh has no closest-point query — only ray Intersect/IsOccluded, plus
IntersectSphere, which is a boolean overlap test and NOT a nearest lookup. We
use tinybvh as the *builder* and traverse it ourselves in print_remesh.cpp.

That traversal reads the public members `BVH::bvhNode` and `BVH::primIdx` and
relies on the Wald 32-byte node layout:

    leaf     triCount > 0, primitives are primIdx[leftFirst .. leftFirst+triCount-1]
    interior triCount == 0, children are nodes leftFirst and leftFirst + 1
    root     node 0

Confirmed against BVH::Build (tiny_bvh.h, the node-split block that assigns
bvhNode[lci]/bvhNode[rci] and clears node.triCount). This is a deliberate
coupling to an internal-looking surface — re-check it when bumping the version,
do not assume it still holds.
