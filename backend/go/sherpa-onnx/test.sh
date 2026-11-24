#!/bin/bash
# Unit tests for the sherpa-onnx backend. Exercises error-path and
# dispatch logic via SherpaBackend directly (no gRPC). Integration
# coverage (gRPC TTS / streaming ASR / realtime pipeline) lives in
# tests/e2e-backends and tests/e2e and runs against the Docker image.
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

PACKAGES=$(go list ./... | grep -v /sources/)
go test -v -timeout 60s $PACKAGES
