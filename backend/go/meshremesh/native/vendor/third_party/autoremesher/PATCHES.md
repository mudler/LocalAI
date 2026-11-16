# Local modifications to the vendored AutoRemesher core

Every deviation from the upstream 1.0.0 sources is listed here, so a later
upstream bump is a diffable operation. Files under `src/AutoRemesher/`,
`include/AutoRemesher/`, and `isotropicremesher/` are **never reformatted**
(`format_code.sh` excludes `third_party/`).

Two of the three changes the plan anticipated turned out to be unnecessary;
they are recorded below as "no patch" so nobody goes looking for them.

## 1. TBB — no source patch

The core includes `<tbb/blocked_range.h>`, `<tbb/parallel_for.h>`,
`<tbb/mutex.h>`, and `<tbb/combinable.h>`, and `autoremesher.cpp` prefers the
oneAPI paths via `__has_include`.

Rather than editing those includes, `compat/tbb/*.h` supply exactly those
header names and forward to `t2_tbb_shim.h`, our own MIT `std::thread`
implementation. Adding `third_party/autoremesher/compat` to the include path is
sufficient; `__has_include(<oneapi/tbb/...>)` fails, so the sources fall back to
the legacy `<tbb/...>` names and land on the shim.

**Upstream sources are unmodified for this.**

Verified API surface actually used by the vendored code — smaller than the plan
assumed:

| Name | Used? |
|---|---|
| `tbb::blocked_range<size_t>(begin, end)` | yes, two-argument form only |
| `tbb::parallel_for(range, body)` | yes, two-argument form only (no partitioner) |
| `tbb::mutex` | included, never used |
| `tbb::combinable` | included, never used |

The shim implements all four so the compat headers are truthful. Behavioural
differences from real TBB (no task stealing, nesting handled by a global worker
budget, exceptions captured and rethrown after join) are documented in
`t2_tbb_shim.h`.

## 2. Eigen `AccelerateSupport` — no patch needed

`constrainedleastsquares.cpp` already guards the Apple-only include with
`#if AUTO_REMESHER_USE_ACCELERATE`. We never define that macro, and we do not
adopt the upstream macOS `NL_USE_BLAS` / `-framework Accelerate` block from
`autoremesher.pro`. Nothing to change in the sources.

## 3. `std::cerr` → `T2_AR_LOG` — mechanical substitution

The core writes progress, per-island failures, and a timing breakdown straight
to `std::cerr`. In a library that the Go demo server loads this is log noise, so
every occurrence is redirected to a sink that is silent unless
`TRELLIS2_AUTOREMESHER_VERBOSE` is set in the environment.

Applied as a reproducible substitution, not hand edits — replay it verbatim
after an upstream bump:

```sh
# in third_party/autoremesher/
for f in src/AutoRemesher/autoremesher.cpp \
         src/AutoRemesher/parameterizer.cpp \
         src/AutoRemesher/quadextractor.cpp; do
  sed -i 's/std::cerr/T2_AR_LOG/g' "$f"
  sed -i '0,/^#include /s|^#include |#include "../../t2_ar_log.h"\n#include |' "$f"
done
for f in isotropicremesher/isotropichalfedgemesh.cpp \
         isotropicremesher/isotropicremesher.cpp; do
  sed -i 's/std::cerr/T2_AR_LOG/g' "$f"
  sed -i '0,/^#include /s|^#include |#include "../t2_ar_log.h"\n#include |' "$f"
done
```

Touched: 5 files — one added include each, plus the `std::cerr` occurrences
(including the commented-out ones, which the substitution does not distinguish;
that is intentional, so the sed stays simple and replayable).

`std::cout` is left alone: the only occurrences in the vendored tree are inside
comments in `isotropicremesher/isotropicremesher.cpp`.

## Files added by us (not upstream)

| File | Purpose |
|---|---|
| `t2_tbb_shim.h` | `std::thread` stand-in for the used TBB surface |
| `compat/tbb/*.h` | resolve the upstream `<tbb/...>` includes onto the shim |
| `t2_ar_log.{h,cpp}` | the `T2_AR_LOG` sink |
| `VERSION`, `PATCHES.md` | provenance |
