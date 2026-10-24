# Local native LFM schema regression

`patches/0003-lfm-strict-schema.patch` targets llama.cpp
`c0fd78626405c6d9b934efef0ec7291fcc20e3d6`. It preserves the native strict
flag through tool parsing and serialization, then selects a schema-aware
Python-style argument parser for strict tools. Non-strict tools retain the
legacy builder. This patch is carried by LocalAI; no upstream submission is
required.

The supported subset covers Sound's object properties, required fields,
optional omission, string enums, string lengths, bounded integer/decimal
values and its musical-meter pattern. Unsupported schema constructs fail
explicitly rather than silently dropping constraints. Strict generation uses
canonical property order and decimal notation. The parser also accepts valid
reordered arguments, equivalent escaped enum strings and in-range exponent
notation, and validates their decoded values. LFM final responses must have a
complete native envelope before tools are published, including non-strict calls.
Partial streaming and non-strict argument recognition remain unchanged.

## CPU-only checks

Use an isolated extraction of the exact pinned source. Apply the patch with
`git apply --check` followed by `git apply` inside an initialized isolated
source repository. The runner neither downloads nor patches source. Do not
run Git patch checks from an uninitialized directory inside another repo:
Git can resolve that enclosing repository instead of the intended source.

From the LocalAI root:

```sh
backend/cpp/llama-cpp/tests/run-native-strict.sh /absolute/pinned-source /absolute/scratch-build
```

The runner builds the actual `llama-common` and `llama` CPU libraries and runs
all three CTest executables. It requires no model, inference, GPU or gRPC server.
Rebuild dependent objects after changes to the native tool struct. The
harness names deliberately do not match the standard-library-only backend
runner's `*_test.cpp` discovery pattern.

`native-strict` checks strict true/false/absent round trips and legacy
three-field aggregate initialization. `native-schema` uses the committed
`fixtures/sound-schema.json`, derived from LocalAI's Sound tool definition,
and exercises generated grammar recognition, complete PEG parsing and mapped
tool calls separately. This fixture is covered by the repository's MIT
license. It includes all 504 existing keyscale values.

The baseline accepts 30 invalid cases in the original 44-case matrix. The
expanded patched matrix currently has 1,092 cases, including all enum values
under both parallel settings. `native-edge` adds 639 checks for semantic edges,
required/auto/none modes, strict true/false/absent and bytewise final truncation,
plus six JSON-comparison controls. String repetitions use bounded chunks because
the native GBNF reader treats repetition bounds above 2,000 as unbounded.
These are offline parser checks, not tokenized sampling or model benchmarks.

## Maintenance and remaining integration

Keep the native patch tied to the exact pin above. Rebase and rerun transport,
grammar and compatibility checks whenever that pin changes. Do not assume
an upstream update fixes this behavior without running the actual LFM path.
Production patch preparation, full schema compatibility review, additional
regressions, performance comparison, duplicate-key defense and CI integration
are separate acceptance steps. Do not treat this harness alone as a deployed
or complete repair. No inference-throughput improvement is claimed.
