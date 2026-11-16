# SkinTokens native backend

This adapter uses the pinned `localai-org/skin-tokens.cpp` C ABI for generated
rigs, learned skinning weights on supplied skeletons, and SOMA30 motion retargeting.
Requests use the existing `Animate3D` transport with named file inputs:

| Inputs | Operation |
| --- | --- |
| `mesh` | Generate a skeleton and learned weights |
| `mesh`, `skeleton` | Learn weights for a supplied static or animated skeleton |
| `mesh`, `motion` | Apply SOMA30 motion to a recognized generated humanoid rig |

The last operation preserves upstream's default confidence threshold; it does
not promise arbitrary-creature animation. A failed native operation never
publishes its partially written output. Loading requires an actual model bundle;
the adapter does not accept arbitrary model names during backend probing.

Build with `make` (CPU) or `make BUILD_TYPE=vulkan`. C++23, CMake 3.25+, Go,
and nlohmann-json 3.11+ are required. Linux amd64/arm64 and Darwin arm64 CPU
are build targets. Metal is disabled because upstream's explicit device choices
are CPU/Vulkan. `make test` runs contract tests; set `SKINTOKENS_LIBRARY` to the
built shared library to also check the real native ABI and invalid-model handling.

Material transfer retains embedded textures, UVs, vertex colours and PBR material
settings for single-primitive GLBs (the generated asset format). Node transforms
are baked by the native exporter. Multiple mesh instances/primitives, morph
targets, compressed geometry and external texture URLs are rejected explicitly;
they are not silently converted to untextured meshes. Generated rigs retain the
native hierarchy and weights. Old unused motion buffers are not copied forward.

The `skintokens-f16` model entry and importer pin the three published F16 files
by revision and SHA-256. The backend library covers Linux CPU/Vulkan and Darwin
CPU; newly added OCI tags require a build before they can be installed remotely.
Real-model acceptance covers rig generation, learned supplied-skeleton weights,
material preservation and SOMA30 retargeting. Set `SKINTOKENS_TEST_BUNDLE`,
`SKINTOKENS_TEST_MESH`, `SKINTOKENS_TEST_MOTION` and optionally
`SKINTOKENS_TEST_DEVICE=vulkan` alongside the library path to run it. Discovery
advertises the distinct mesh/skeleton/motion input
combinations through `three_d_operations`; it does not advertise text input.
