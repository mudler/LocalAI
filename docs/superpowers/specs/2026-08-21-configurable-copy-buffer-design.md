# Configurable copy buffer design

**Date:** 21 August 2026
**Status:** Approved

## Problem

`pkg/xio.Copy` wraps a source reader so a context can stop a copy between
reads. It delegates to `io.Copy`, which uses a 32 KiB buffer for the wrapped
reader and writer types used by model downloads.

Small writes limit model import throughput when the models directory uses an
SMB volume. The development deployment reads large files from the volume at
about 104 MiB/s. A model import writes to the same volume at less than 1 MiB/s.

## Design

Keep `xio.Copy` as the context-aware copy entry point. Add variadic functional
options so existing callers continue to compile without changes.

Add an exported `Option` type and a `WithBufferSize(size int) Option` function.
`Copy` uses a 1 MiB buffer by default. A caller can override the buffer size
with `WithBufferSize`.

If a caller supplies a non-positive buffer size, `Copy` uses the 1 MiB default.
This rule prevents invalid configuration from causing an `io.CopyBuffer`
panic.

`Copy` allocates one buffer for each active call. It passes that buffer to
`io.CopyBuffer`. The context-aware reader continues to check cancellation
before each source read.

The first change does not use `sync.Pool`. A pool adds shared state and retains
large caller-selected buffers. Measurements do not justify that complexity.

## Compatibility

The existing signature gains only a variadic argument:

```go
func Copy(ctx context.Context, dst io.Writer, src io.Reader, options ...Option) (int64, error)
```

All existing calls remain source compatible. Copy results and cancellation
errors do not change.

The default buffer increases temporary memory use by approximately 992 KiB for
each concurrent copy compared with the current 32 KiB buffer.

## Tests and measurement

Add a Ginkgo suite for `pkg/xio`. Tests cover these behaviors:

- `Copy` copies the complete source.
- The default buffer permits reads larger than 32 KiB.
- `WithBufferSize` changes the maximum requested read size.
- A non-positive override uses the default buffer.
- A canceled context stops the copy and returns the context error.

Add a benchmark that runs `Copy` with the default buffer and representative
overrides. The benchmark records throughput and allocations. It does not make
timing assertions.

Run the focused `pkg/xio` suite first. Then run the packages that call
`xio.Copy`: `pkg/downloader` and `pkg/oci`.

## Deployment validation

The code change alone does not alter the running development deployment. After
CI publishes a development image and Flux deploys it, import a large model to
the NAS-backed models directory. Compare the progress rate with the previous
0.7-0.8 MiB/s result.

