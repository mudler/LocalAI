# Native strict transport regression

`patches/0003-lfm-strict-schema.patch` targets llama.cpp
`c0fd78626405c6d9b934efef0ec7291fcc20e3d6`. This first portion changes
`common/chat.h` and `common/chat.cpp` only. It preserves `function.strict:true`
through the actual oaicompat parser, `common_chat_tool`, and serializer.
False and absent values both default to non-strict. Their serialized tool
representation does not change. Three-field aggregate initializers remain
valid because the new trailing member defaults to false.

This patch does **not** enforce argument schemas or change LFM grammar.
Strict schema generation, production patch preparation, and CI integration
remain separate work. Do not treat this transport regression as full schema
acceptance.

## CPU-only check

Use an isolated extraction of the exact pinned source. Apply the patch with
`git apply --check` followed by `git apply` from that source directory. The
runner neither downloads nor patches source, so it also runs against an
unpatched extraction for the RED check.

From the LocalAI root:

```sh
backend/cpp/llama-cpp/tests/run-native-strict.sh /absolute/pinned-source /absolute/scratch-build
```

The runner builds the real `llama-common` and `llama` libraries with CPU-only
settings, then runs CTest. It requires no model, gRPC server, inference, or
GPU. Use a fresh build directory when changing source trees. A change to the
native struct requires rebuilding dependent common objects; do not link
old common libraries against the new header.

The harness is deliberately not named `*_test.cpp`: the independent
standard-library-only backend runner must not discover it.

At the unpatched pin the executable builds, then fails three strict-true
round-trip checks. False, absent, aggregate compatibility, and empty/null
controls pass. After the patch, all checks pass.

## Maintenance

Keep this patch tied to the exact pin above. Rebase and rerun this native
regression when the dependency changes. Retire the transport portion only
when the new pin preserves strict through both native conversion functions
and passes the legacy compatibility controls. Grammar enforcement requires
its own acceptance tests before any full-repair claim.
