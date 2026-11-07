# run.ps1 - backend launcher for the native windows/amd64 llama-cpp backend
# images. The mirror of ../run.sh for a host with no POSIX shell: pick the best
# binary for the host, set up the library search path, then run it, forwarding
# arguments and exit code. pkg/model/process.go starts it via
# `powershell.exe -NoProfile -ExecutionPolicy Bypass -File run.ps1`.

$ErrorActionPreference = "Stop"

$curDir = Split-Path -Parent $MyInvocation.MyCommand.Path

$binary = "llama-cpp-fallback.exe"
if (Test-Path (Join-Path $curDir "llama-cpp-cpu-all.exe")) {
    $binary = "llama-cpp-cpu-all.exe"
}

if ($env:LLAMACPP_GRPC_SERVERS) {
    if (Test-Path (Join-Path $curDir "llama-cpp-grpc.exe")) {
        $binary = "llama-cpp-grpc.exe"
    }
}

# ggml's shared backends (libggml-cpu-*.dll) live next to the executable so
# ggml's own registry finds them; a lib\ dir is still honoured when a variant
# ships one, mirroring the run.sh LD_LIBRARY_PATH handling.
$lib = Join-Path $curDir "lib"
if (Test-Path $lib) {
    $env:PATH = "$lib;$env:PATH"
}

& (Join-Path $curDir $binary) @args
exit $LASTEXITCODE
