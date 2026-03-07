#!/usr/bin/env python3
"""
LocalAI gRPC backend for Meta Spirit LM: interleaved text and speech model.
Supports text generation (Predict), TTS, and audio transcription (ASR).
"""
from concurrent import futures
import argparse
import io
import os
import signal
import struct
import sys
import time
from typing import Any, Dict, List, Optional

import backend_pb2
import backend_pb2_grpc
import grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))
DEFAULT_SAMPLE_RATE = 16000


def _parse_option_value(value: str) -> Any:
    if value.lower() in ("true", "false"):
        return value.lower() == "true"
    try:
        return int(value)
    except ValueError:
        pass
    try:
        return float(value)
    except ValueError:
        pass
    return value


def _float32_audio_to_wav_bytes(audio: Any, sample_rate: int = DEFAULT_SAMPLE_RATE) -> bytes:
    """Convert float32 mono audio array to WAV bytes."""
    import numpy as np
    samples = np.asarray(audio, dtype=np.float32)
    if samples.ndim != 1:
        samples = samples.flatten()
    n_frames = len(samples)
    n_channels = 1
    sample_width = 2  # 16-bit
    byte_rate = sample_rate * n_channels * sample_width
    block_align = n_channels * sample_width
    data_size = n_frames * sample_width
    # Clip to [-1, 1] and convert to int16
    samples = samples.clip(-1.0, 1.0)
    int16_samples = (samples * 32767).astype("<i2")
    buf = io.BytesIO()
    buf.write(b"RIFF")
    buf.write(struct.pack("<I", 36 + data_size))
    buf.write(b"WAVE")
    buf.write(b"fmt ")
    buf.write(struct.pack("<IHHIIHH", 16, 1, n_channels, sample_rate, byte_rate, block_align, 16))
    buf.write(b"data")
    buf.write(struct.pack("<I", data_size))
    buf.write(int16_samples.tobytes())
    return buf.getvalue()


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer implementing LocalAI backend for Spirit LM."""

    def Health(self, request: backend_pb2.HealthMessage, context: grpc.ServicerContext) -> backend_pb2.Reply:
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request: backend_pb2.ModelOptions, context: grpc.ServicerContext) -> backend_pb2.Result:
        try:
            from spiritlm.model.spiritlm_model import Spiritlm
        except ImportError as e:
            return backend_pb2.Result(success=False, message=f"SpiritLM not installed: {e}")

        self._options = self._parse_options(request.Options)
        model_name = (request.Model or "").strip() or "spirit-lm-base-7b"
        if model_name not in ("spirit-lm-base-7b", "spirit-lm-expressive-7b"):
            return backend_pb2.Result(success=False, message=f"Unknown model: {model_name}")

        try:
            print(f"Loading Spirit LM model: {model_name}", file=sys.stderr)
            self._model = Spiritlm(model_name)
            self._sample_rate = self._options.get("sample_rate", DEFAULT_SAMPLE_RATE)
            print("Spirit LM model loaded successfully", file=sys.stderr)
        except Exception as e:
            print(f"LoadModel failed: {e}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(e))

        return backend_pb2.Result(success=True, message="Model loaded successfully")

    def _parse_options(self, options: List[str]) -> Dict[str, Any]:
        out: Dict[str, Any] = {}
        for opt in options or []:
            if ":" not in opt:
                continue
            key, _, value = opt.partition(":")
            key = key.strip()
            value = value.strip()
            if key:
                out[key] = _parse_option_value(value)
        return out

    def _generation_config(
        self,
        tokens: int = 200,
        temperature: float = 0.9,
        top_p: float = 0.95,
        do_sample: bool = True,
    ) -> Any:
        from transformers import GenerationConfig
        return GenerationConfig(
            max_new_tokens=tokens,
            temperature=temperature,
            top_p=top_p,
            do_sample=do_sample,
        )

    def Predict(self, request: backend_pb2.PredictOptions, context: grpc.ServicerContext) -> backend_pb2.Reply:
        if not getattr(self, "_model", None):
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("Model not loaded")
            return backend_pb2.Reply(message=b"")
        try:
            text = self._generate_text(
                prompt=request.Prompt or "",
                tokens=max(1, request.Tokens or 200),
                temperature=request.Temperature if request.Temperature > 0 else 0.9,
                top_p=request.TopP if request.TopP > 0 else 0.95,
                stop_prompts=list(request.StopPrompts) if request.StopPrompts else None,
            )
            return backend_pb2.Reply(
                message=text.encode("utf-8"),
                tokens=len(text.split()),
                prompt_tokens=0,
            )
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return backend_pb2.Reply(message=b"")

    def PredictStream(
        self, request: backend_pb2.PredictOptions, context: grpc.ServicerContext
    ) -> Any:
        if not getattr(self, "_model", None):
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("Model not loaded")
            return
        try:
            text = self._generate_text(
                prompt=request.Prompt or "",
                tokens=max(1, request.Tokens or 200),
                temperature=request.Temperature if request.Temperature > 0 else 0.9,
                top_p=request.TopP if request.TopP > 0 else 0.95,
                stop_prompts=list(request.StopPrompts) if request.StopPrompts else None,
            )
            yield backend_pb2.Reply(message=text.encode("utf-8"), tokens=len(text.split()))
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))

    def _generate_text(
        self,
        prompt: str,
        tokens: int = 200,
        temperature: float = 0.9,
        top_p: float = 0.95,
        stop_prompts: Optional[List[str]] = None,
    ) -> str:
        from spiritlm.model.spiritlm_model import ContentType, GenerationInput, OutputModality

        if not prompt.strip():
            return ""

        inputs = [GenerationInput(content=prompt.strip(), content_type=ContentType.TEXT)]
        config = self._generation_config(tokens=tokens, temperature=temperature, top_p=top_p)
        outputs = self._model.generate(
            output_modality=OutputModality.TEXT,
            interleaved_inputs=inputs,
            generation_config=config,
        )
        parts: List[str] = []
        for out in outputs or []:
            if getattr(out, "content_type", None) and str(getattr(out.content_type, "name", "")) == "TEXT":
                content = getattr(out, "content", None)
                if isinstance(content, str):
                    parts.append(content)
        result = "".join(parts)
        if stop_prompts:
            for stop in stop_prompts:
                if stop in result:
                    result = result.split(stop)[0].strip()
        return result

    def AudioTranscription(
        self, request: backend_pb2.TranscriptRequest, context: grpc.ServicerContext
    ) -> backend_pb2.TranscriptResult:
        if not getattr(self, "_model", None):
            return backend_pb2.TranscriptResult(segments=[], text="")
        audio_path = (request.dst or "").strip()
        if not audio_path or not os.path.isfile(audio_path):
            print(f"Audio file not found: {audio_path}", file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

        try:
            from spiritlm.model.spiritlm_model import ContentType, GenerationInput, OutputModality

            inputs = [GenerationInput(content=audio_path, content_type=ContentType.SPEECH)]
            config = self._generation_config(tokens=500, temperature=0.2, top_p=0.95)
            outputs = self._model.generate(
                output_modality=OutputModality.TEXT,
                interleaved_inputs=inputs,
                generation_config=config,
            )
            parts: List[str] = []
            for out in outputs or []:
                if getattr(out, "content_type", None) and str(getattr(out.content_type, "name", "")) == "TEXT":
                    content = getattr(out, "content", None)
                    if isinstance(content, str):
                        parts.append(content)
            text = " ".join(parts).strip()
            segment = backend_pb2.TranscriptSegment(id=0, start=0, end=0, text=text)
            return backend_pb2.TranscriptResult(segments=[segment], text=text)
        except Exception as e:
            print(f"AudioTranscription failed: {e}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

    def TTS(self, request: backend_pb2.TTSRequest, context: grpc.ServicerContext) -> backend_pb2.Result:
        if not getattr(self, "_model", None):
            return backend_pb2.Result(success=False, message="Model not loaded")
        text = (request.text or "").strip()
        if not text:
            return backend_pb2.Result(success=False, message="TTS request has no text")

        try:
            from spiritlm.model.spiritlm_model import ContentType, GenerationInput, OutputModality

            inputs = [GenerationInput(content=text, content_type=ContentType.TEXT)]
            config = self._generation_config(tokens=400, temperature=0.9, top_p=0.95)
            outputs = self._model.generate(
                output_modality=OutputModality.SPEECH,
                interleaved_inputs=inputs,
                generation_config=config,
            )
            audio_float32 = None
            for out in outputs or []:
                if getattr(out, "content_type", None) and str(getattr(out.content_type, "name", "")) == "SPEECH":
                    content = getattr(out, "content", None)
                    if content is not None and hasattr(content, "__len__"):
                        import numpy as np
                        arr = np.asarray(content, dtype=np.float32)
                        if audio_float32 is None:
                            audio_float32 = arr
                        else:
                            audio_float32 = np.concatenate([audio_float32, arr])
            if audio_float32 is None:
                return backend_pb2.Result(success=False, message="No speech output from model")

            wav_bytes = _float32_audio_to_wav_bytes(audio_float32, self._sample_rate)
            if request.dst:
                with open(request.dst, "wb") as f:
                    f.write(wav_bytes)
            return backend_pb2.Result(success=True, message="OK")
        except Exception as e:
            print(f"TTS failed: {e}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(e))

    def TTSStream(
        self, request: backend_pb2.TTSRequest, context: grpc.ServicerContext
    ) -> Any:
        if not getattr(self, "_model", None):
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("Model not loaded")
            return
        result = self.TTS(request, context)
        if not result.success:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(result.message)
            return
        if request.dst and os.path.isfile(request.dst):
            with open(request.dst, "rb") as f:
                data = f.read()
            yield backend_pb2.Reply(audio=data)


def serve(address: str) -> None:
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 50 * 1024 * 1024),
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Spirit LM backend listening on: " + address, file=sys.stderr)

    def signal_handler(sig: int, frame: Any) -> None:
        print("Shutting down...", file=sys.stderr)
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Spirit LM gRPC backend for LocalAI")
    parser.add_argument("--addr", default="localhost:50051", help="Address to bind")
    args = parser.parse_args()
    serve(args.addr)
