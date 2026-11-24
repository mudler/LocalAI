# Model-free native fixture

`omnivoice.h` is an unmodified public header from ServeurpersoCom/omnivoice.cpp,
commit `b549668434fbe0a8592ae26adc08c1aa4e329805` (the backend's pinned engine).
Source: https://raw.githubusercontent.com/ServeurpersoCom/omnivoice.cpp/b549668434fbe0a8592ae26adc08c1aa4e329805/src/omnivoice.h
SHA-256: `891bbccd2ab63b7ce2e2cb8a35b0f57a347198086d9f003dbd98a164f0b1261a`.

`LICENSE.omnivoice` retains the upstream MIT license from the same commit:
https://raw.githubusercontent.com/ServeurpersoCom/omnivoice.cpp/b549668434fbe0a8592ae26adc08c1aa4e329805/LICENSE
SHA-256: `cddbecd5db98ec5bd44af3e7221b6d44c3e9fed1c92940a3be5b0080c9b86475`.

The verification script compiles the production wrapper against this header.
`engine.cpp` implements only the engine calls the wrapper needs. Its initializer
uses the documented defaults. Captures copy borrowed strings and reference data
before returning. Its fake context never opens a model. Synthetic PCM exercises
allocation, copying, freeing, and callbacks, not inference or audio quality.
`ggml-backend.h` supplies only the wrapper's logging and backend declarations.

Run `bash backend/go/omnivoice-cpp/verify-controls.sh` from an offline Go setup
with generated protobuf files. The script requires Linux and a C++17 compiler.
No engine build, models, services, or downloads run during verification.
