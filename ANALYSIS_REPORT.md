# Fish-Speech Backend Integration Analysis Report

**Date:** 2026-03-10  
**Analyst:** claude-agent-2 (Autonomous Orchestrator)  
**Task:** Preliminary Analysis - Add fish-speech Backend to LocalAI  
**Status:** Analysis Complete

---

## Executive Summary

This report provides a comprehensive analysis of integrating the Fish-Speech S2 (S2-Pro) text-to-speech model into LocalAI as a new backend. Fish-Speech is a state-of-the-art open-source TTS system with 4B parameters that supports multilingual speech synthesis, voice cloning, and fine-grained emotional/prosodic control.

**Recommendation:** **GO** - Integration is feasible with moderate complexity. Fish-Speech's HTTP API and Dual-AR architecture make it suitable for LocalAI backend integration.

---

## 1. Fish-Speech Technical Summary

### 1.1 Repository Overview
- **GitHub:** https://github.com/fishaudio/fish-speech
- **Stars:** 25.3k+ | **Forks:** 2.1k+
- **License:** FISH AUDIO RESEARCH LICENSE (commercial use restrictions apply)
- **Language:** Python 95%
- **Latest Release:** v1.5.1 (May 2025)

### 1.2 Model Architecture
- **Model:** Fish Audio S2-Pro (4B parameters)
- **Architecture:** Dual-Autoregressive (Dual-AR) with decoder-only transformer
- **Audio Codec:** RVQ-based (10 codebooks, ~21 Hz frame rate)
- **Training Data:** 10M+ hours across ~50 languages
- **Performance:**
  - Real-Time Factor (RTF): 0.195 on NVIDIA H200
  - Time-to-first-audio: ~100ms
  - Throughput: 3,000+ acoustic tokens/s

### 1.3 API Endpoints

The fish-speech repository provides an HTTP API server at `tools/api_server.py`:

#### Server Startup
```bash
python tools/api_server.py \
  --llama-checkpoint-path checkpoints/s2-pro \
  --decoder-checkpoint-path checkpoints/s2-pro/codec.pth \
  --listen 0.0.0.0:8080
```

#### Available Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/health` | GET | Health check |
| `/v1/tts` | POST | Text-to-speech generation |
| `/v1/vqgan/encode` | POST | VQ encode |
| `/v1/vqgan/decode` | POST | VQ decode |

#### TTS API Request Format (inferred)
```json
POST /v1/tts
{
  "text": "Text to synthesize",
  "voice": "reference_audio_path or voice_id",
  "language": "en|zh|ja|ko|ar|de|fr|...",
  "temperature": 0.7,
  "duration": null,
  "streaming": false
}
```

#### Response Format
- Returns audio data (WAV format, 24kHz sample rate)
- Supports streaming responses

### 1.4 Key Features

1. **Multilingual Support:** 50+ languages without phoneme preprocessing
2. **Voice Cloning:** 10-30 second reference audio for instant cloning
3. **Fine-Grained Control:** Natural language tags for prosody/emotion:
   - `[laugh]`, `[whispers]`, `[super happy]`
   - `[whisper in small voice]`, `[professional broadcast tone]`
4. **Multi-Speaker Generation:** Native support via `<|speaker:i|>` tokens
5. **Multi-Turn Generation:** Context-aware speech synthesis

### 1.5 Dependencies & Runtime Requirements

**Base Dependencies:**
- Python 3.10+
- PyTorch (CUDA 12/13 recommended)
- Transformers
- Custom fish_speech package

**Hardware Requirements:**
- **Minimum:** GPU with 16GB VRAM (for 4B model)
- **Recommended:** NVIDIA H200/A100 for production throughput
- **CPU:** Possible but significantly slower

**Model Files:**
- `checkpoints/s2-pro/` - Main model weights (4B params)
- `checkpoints/s2-pro/codec.pth` - Audio codec
- Download from: https://huggingface.co/fishaudio/s2-pro

---

## 2. LocalAI Backend Integration Plan

### 2.1 Proposed Backend Structure

**Backend Name:** `fish-speech`  
**Location:** `backend/python/fish-speech/`

```
backend/python/fish-speech/
├── backend.py          # gRPC service implementation
├── install.sh          # Installation script
├── run.sh              # Runtime launcher
├── test.sh             # Test runner
├── test.py             # Unit tests
├── Makefile            # Make targets
├── README.md           # Backend documentation
├── requirements.txt    # Base dependencies
├── requirements-cpu.txt
├── requirements-cublas12.txt
├── requirements-cublas13.txt
├── requirements-mps.txt
└── requirements-intel.txt
```

### 2.2 Interface Mapping (fish-speech → LocalAI)

| LocalAI gRPC Method | fish-speech HTTP API | Mapping Notes |
|---------------------|---------------------|---------------|
| `TTS(TTSRequest)` | `POST /v1/tts` | Direct mapping |
| `TTSStream(TTSRequest)` | `POST /v1/tts` (streaming) | Enable streaming mode |
| `Health(HealthMessage)` | `GET /v1/health` | Health check |
| `LoadModel(ModelOptions)` | Model initialization | Load S2-Pro checkpoint |

#### TTSRequest Mapping

```protobuf
// LocalAI TTSRequest
message TTSRequest {
  string text = 1;           // → "text" field
  string model = 2;          // → model selection (s2-pro)
  string dst = 3;            // → output file path
  string voice = 4;          // → voice/reference audio
  optional string language = 5;  // → language code
}
```

### 2.3 Required Code Changes in LocalAI

#### A. Backend Implementation (`backend/python/fish-speech/backend.py`)

The backend will act as a gRPC-to-HTTP proxy:

1. **gRPC Service Implementation:**
   - Implement `BackendServicer` with `Health()`, `LoadModel()`, `TTS()` methods
   - Spawn internal HTTP client to communicate with fish-speech API server
   - Handle audio file I/O as per LocalAI conventions

2. **Model Loading:**
   - Download/model path configuration via `ModelOptions`
   - Initialize connection to fish-speech HTTP server
   - Validate model availability

3. **TTS Generation:**
   - Convert gRPC `TTSRequest` to HTTP POST request
   - Stream/download audio response
   - Write to `dst` file path
   - Return success/failure status

#### B. Configuration Options

Add to LocalAI configuration schema:
```yaml
fish_speech:
  enabled: true
  api_endpoint: "http://localhost:8080"
  api_key: ""  # Optional bearer token
  checkpoint_path: "/path/to/checkpoints/s2-pro"
  decoder_path: "/path/to/checkpoints/s2-pro/codec.pth"
  compile: false  # Enable torch.compile
  half_precision: false  # FP16 mode
  workers: 1  # Process count
```

### 2.4 Communication Protocol

```
┌─────────────────┐     gRPC      ┌──────────────────┐    HTTP     ┌─────────────────┐
│   LocalAI Core  │ ◄──────────► │ fish-speech      │ ──────────► │ fish-speech     │
│                 │              │ Backend (Python) │             │ API Server      │
│ TTS Request     │              │                  │             │ (tools/api_)    │
│ TTS Response    │              │ gRPC-to-HTTP     │             │ s2-pro Model    │
└─────────────────┘              │ Proxy            │             └─────────────────┘
                                 └──────────────────┘
```

---

## 3. Testing Strategy

### 3.1 Unit Test Requirements

Based on existing backends (e.g., kokoro), tests should cover:

1. **Server Startup Test:**
   - Verify gRPC server binds to address
   - Health check returns "OK"

2. **Model Loading Test:**
   - `LoadModel()` succeeds with valid checkpoint paths
   - Proper error handling for missing models

3. **TTS Generation Test:**
   - Generate audio from text
   - Verify output file exists and is valid WAV
   - Test different voices/languages

4. **Stream Test (optional phase 2):**
   - Verify streaming TTS works correctly

### 3.2 Test File Structure (`test.py`)

```python
import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc
import grpc
import soundfile as sf
import os

class TestFishSpeechBackend(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50051"]
        )
        time.sleep(30)  # Wait for model load

    def tearDown(self):
        self.service.terminate()
        self.service.wait()

    def test_health(self):
        with grpc.insecure_channel("localhost:50051") as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            response = stub.Health(backend_pb2.HealthMessage())
            self.assertEqual(response.message, b'OK')

    def test_tts_generation(self):
        # Load model
        # Generate TTS
        # Verify output file
        pass
```

### 3.3 CI/CD Modifications

**GitHub Actions Workflow Additions:**

1. **Backend Build Test:**
   ```yaml
   - name: Test fish-speech backend build
     run: |
       cd backend/python/fish-speech
       make test
   ```

2. **Integration Test:**
   - Requires GPU runner or mock model
   - Test end-to-end TTS pipeline

3. **Linting/Formatting:**
   - Python black/isort checks
   - flake8/pylint

### 3.4 Test Fixtures Requirements

- Small test audio files for voice reference
- Sample text inputs in multiple languages
- Expected output audio files (baseline)

---

## 4. Implementation Roadmap

### Phase 1: Core Backend (2-3 weeks)

**Week 1: Foundation**
- Create backend directory structure
- Implement basic `backend.py` with gRPC service skeleton
- Write `install.sh` and `run.sh` scripts
- Create `requirements.txt` with dependencies

**Week 2: gRPC-to-HTTP Proxy**
- Implement HTTP client for fish-speech API
- Map `TTSRequest` to HTTP POST
- Handle audio file I/O
- Add error handling and logging

**Week 3: Testing & Documentation**
- Write unit tests (`test.py`, `test.sh`)
- Create `README.md` with usage instructions
- Test locally with fish-speech server

### Phase 2: Advanced Features (1-2 weeks)

**Week 4: Streaming Support**
- Implement `TTSStream()` method
- Add streaming HTTP client support
- Test streaming latency

**Week 5: Configuration & Optimization**
- Add configuration options (api_key, compilation, etc.)
- Performance optimization
- Memory management improvements

### Phase 3: Integration & CI (1 week)

**Week 6: LocalAI Integration**
- Register backend in LocalAI core
- Add configuration schema
- Update documentation

**Week 7: CI/CD & Release**
- Add GitHub Actions workflows
- Run full test suite
- Create release PR

### Risk Assessment

| Risk | Severity | Mitigation |
|------|----------|------------|
| License restrictions | HIGH | Review FISH AUDIO license carefully; document usage restrictions |
| GPU memory requirements | MEDIUM | Document minimum hardware; provide CPU fallback option |
| API compatibility changes | MEDIUM | Version pinning; abstraction layer for HTTP client |
| Model download complexity | LOW | Provide download script; cache management |
| Streaming complexity | LOW | Defer to Phase 2; start with non-streaming |

### Estimated Effort Breakdown

| Task | Hours | Notes |
|------|-------|-------|
| Backend implementation | 20-30 | Core gRPC service |
| HTTP proxy logic | 10-15 | API communication |
| Testing | 10-15 | Unit/integration tests |
| Documentation | 5-8 | README, config docs |
| CI/CD setup | 5-8 | GitHub Actions |
| Integration | 8-12 | LocalAI core changes |
| **Total** | **58-88** | ~2-3 weeks part-time |

### Dependencies & Prerequisites

1. **External:**
   - Fish-speech HTTP API server running
   - Model weights downloaded from HuggingFace
   - GPU with sufficient VRAM (16GB+)

2. **Internal (LocalAI):**
   - gRPC protobuf generation working
   - Python backend infrastructure functional
   - Configuration system supports new options

---

## 5. Recommendation

### GO/NO-GO: **GO** ✅

**Rationale:**
1. Fish-Speech provides a clean HTTP API suitable for gRPC proxying
2. Architecture aligns well with existing LocalAI Python backends (e.g., kokoro)
3. Strong community adoption (25k+ stars) indicates stability
4. Multilingual support adds significant value to LocalAI
5. Implementation complexity is moderate (similar to existing TTS backends)

### Alternative Approaches

1. **Direct Integration (Not Recommended):**
   - Embed fish-speech code directly into backend
   - **Pros:** No HTTP overhead
   - **Cons:** Complex dependencies, harder to maintain

2. **External Service Only:**
   - Use Fish Audio cloud API instead
   - **Pros:** No model hosting required
   - **Cons:** Privacy concerns, cost, latency

3. **Phased Rollback Plan:**
   - Start with HTTP proxy (Phase 1)
   - If complexity arises, fall back to external API mode
   - Document limitations clearly

### Suggested Next Steps

1. **Immediate:**
   - Create GitHub issue for tracking
   - Assign developer for Phase 1 implementation
   - Review license restrictions with legal team

2. **Short-term (1 week):**
   - Set up test environment with fish-speech server
   - Create initial backend skeleton
   - Validate gRPC-to-HTTP proxy concept

3. **Long-term (1 month):**
   - Complete Phase 1 implementation
   - Submit PR for review
   - Plan Phase 2 features based on feedback

---

## Appendix A: License Notice

**CRITICAL:** The fish-speech codebase and model weights are released under the **FISH AUDIO RESEARCH LICENSE**. This license has specific restrictions on commercial use. Before proceeding with integration:

1. Review the full license text: https://github.com/fishaudio/fish-speech/blob/main/LICENSE
2. Determine if intended use cases comply with license terms
3. Consider alternative models if commercial use is required without restrictions

---

## Appendix B: References

1. **Fish-Speech GitHub:** https://github.com/fishaudio/fish-speech
2. **Technical Report:** https://arxiv.org/abs/2411.01156
3. **Documentation:** https://speech.fish.audio/
4. **Model Weights:** https://huggingface.co/fishaudio/s2-pro
5. **LocalAI Backend Examples:** `backend/python/kokoro/`, `backend/python/coqui/`

---

**Analysis Completed:** 2026-03-10 23:20 UTC  
**Next Review:** Upon implementation completion  
**Contact:** claude-agent-2 (Autonomous Orchestrator)
