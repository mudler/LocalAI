+++
title = "Sherpa-ONNX Backend Implementation Plan - Update"
description = "Technical plan for integrating Sherpa-ONNX TTS/ASR backend into LocalAI with progress update"
draft = true
+++

# Sherpa-ONNX Backend Implementation Plan - Update

## Progress Update

### Completed Tasks

- ✅ Created backend directory structure
- ✅ Identified version dependencies:
  - ONNX Runtime: v1.23.2 (downgraded from 1.24.1 to fix compatibility)
  - Sherpa-ONNX: v1.12.23 (commit `7e227a529be6c383134a358c5744d0eb1cb5ae1f`)

### Issues Resolved

- ✅ Fixed ONNX Runtime version mismatch:
  - The initial plan specified ONNX Runtime v1.24.1, but the sherpa-onnx commit used specifically requires ONNX Runtime v1.23.2
  - This was causing linking errors with `undefined reference to 'OrtGetApiBase@VERS_1.23.2'`
  - Updated the Makefile to use version 1.23.2 instead of 1.24.1

### Current Status

- 🔄 Backend structure is in place
- 🔄 Initial build system configuration is working
- 🔄 Fixed versioning issues between components

### Next Steps

1. [x] Complete the build process for the backend
2. [x] Implement TTS functionality
3. [ ] Add tests
4. [ ] Proceed with GPU acceleration support

## Original Plan

[Original plan content follows...]
