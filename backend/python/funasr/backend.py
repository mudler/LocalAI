#!/usr/bin/env python3
"""
gRPC server of LocalAI for FunASR (SenseVoice, Paraformer, Fun-ASR-Nano).
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import backend_pb2
import backend_pb2_grpc

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        try:
            import torch
            from funasr import AutoModel
        except ImportError as e:
            return backend_pb2.Result(success=False, message=f"Missing dependency: {e}")

        if torch.cuda.is_available():
            device = "cuda"
        elif hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
            device = "mps"
        else:
            device = "cpu"

        if not torch.cuda.is_available() and request.CUDA:
            return backend_pb2.Result(success=False, message="CUDA is not available")

        self.device = device
        self.options = {}
        for opt in request.Options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            self.options[key] = value

        model_path = request.Model or "iic/SenseVoiceSmall"

        vad_model = self.options.get("vad_model", "fsmn-vad")
        spk_model = self.options.get("spk_model", "")
        punc_model = self.options.get("punc_model", "")

        try:
            print(f"Loading FunASR model: {model_path}", file=sys.stderr)

            kwargs = {
                "model": model_path,
                "device": device,
            }

            if vad_model:
                kwargs["vad_model"] = vad_model

            if spk_model:
                kwargs["spk_model"] = spk_model

            if punc_model:
                kwargs["punc_model"] = punc_model

            # Fun-ASR-Nano needs trust_remote_code and hub
            model_lower = model_path.lower()
            if "fun-asr" in model_lower or "funasr-nano" in model_lower:
                kwargs["hub"] = "hf"
                kwargs["trust_remote_code"] = True
                kwargs["vad_kwargs"] = {"max_single_segment_time": 30000}

            self.model = AutoModel(**kwargs)
            print("FunASR model loaded successfully", file=sys.stderr)
        except Exception as err:
            print(f"[ERROR] LoadModel failed: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        try:
            audio_path = request.dst
            if not audio_path or not os.path.exists(audio_path):
                print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
                return backend_pb2.TranscriptResult(segments=[], text="")

            language = None
            if request.language and request.language.strip():
                language = request.language.strip()

            kwargs = {"input": audio_path}
            if language:
                kwargs["language"] = language

            results = self.model.generate(**kwargs)

            if not results:
                return backend_pb2.TranscriptResult(segments=[], text="")

            result = results[0]
            text = result.get("text", "")

            segments = []
            if "timestamp" in result and result["timestamp"]:
                for idx, ts in enumerate(result["timestamp"]):
                    if isinstance(ts, (list, tuple)) and len(ts) >= 2:
                        start_ms = int(ts[0])
                        end_ms = int(ts[1])
                        seg_text = ""
                        if len(ts) >= 3:
                            seg_text = str(ts[2])
                        segments.append(backend_pb2.TranscriptSegment(
                            id=idx,
                            start=start_ms * 1_000_000,
                            end=end_ms * 1_000_000,
                            text=seg_text,
                        ))

            if "sentence_info" in result and result["sentence_info"]:
                for idx, sent in enumerate(result["sentence_info"]):
                    if isinstance(sent, dict):
                        start_ms = int(sent.get("start", 0))
                        end_ms = int(sent.get("end", 0))
                        seg_text = sent.get("text", "")
                        spk = sent.get("spk", -1)
                        segments.append(backend_pb2.TranscriptSegment(
                            id=idx,
                            start=start_ms * 1_000_000,
                            end=end_ms * 1_000_000,
                            text=f"[Speaker {spk}] {seg_text}" if spk >= 0 else seg_text,
                        ))

            if not segments and text:
                segments.append(backend_pb2.TranscriptSegment(
                    id=0, start=0, end=0, text=text
                ))

            return backend_pb2.TranscriptResult(segments=segments, text=text)

        except Exception as err:
            print(f"Error in AudioTranscription: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
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
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    args = parser.parse_args()
    serve(args.addr)
