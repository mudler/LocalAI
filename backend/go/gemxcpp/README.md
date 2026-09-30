# GEM-X backend

Resident native `gemx_live_*` inference via PureGo. See
[Motion Capture](../../../docs/content/features/motion.md) for configuration and
protocol. The upstream pin lives in `Makefile` and is registered with the daily
bump workflow. Linux CPU/Vulkan and Darwin CPU packages are built; Metal is not
supported by the upstream runtime. Model assets are not needed for unit tests.

```sh
make -C backend/go/gemxcpp test
make backends/gemxcpp
```

The adapter owns one native pipeline per loaded model, with one active session.
Each instance loads GEM, ViTPose and YOLOX. No SAM3D Body, mesh export, SONIC,
physics, or Python dependency is introduced. The C library has no immediate
inference cancellation; cancellation discards results and releases the session
when the native call returns. The public stream schema lives in
`pkg/motion/proto/motion.proto`, independently of backend RPC envelopes.
