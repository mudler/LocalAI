// SPDX-License-Identifier: MIT
#pragma once
// The vendored geometry headers use only the export macro, not the inference
// API. Keep this compatibility shim outside the unmodified vendor snapshot.
#if defined(_WIN32)
#define TRELLIS2_API __declspec(dllexport)
#else
#define TRELLIS2_API __attribute__((visibility("default")))
#endif
