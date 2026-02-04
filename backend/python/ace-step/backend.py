#!/usr/bin/env python3
"""
LocalAI ACE-Step Backend

gRPC backend for ACE-Step 1.5 music generation. Aligns with upstream acestep API:
- LoadModel: initializes AceStepHandler (DiT) and LLMHandler, parses Options.
- SoundGeneration: uses create_sample (simple mode), format_sample (optional), then
  generate_music from acestep.inference. Writes first output to request.dst.
- Fail hard: no fallback WAV on error; exceptions propagate to gRPC.
"""
from concurrent import futures
import argparse
import shutil
import signal
import sys
import os
import tempfile

import backend_pb2
import backend_pb2_grpc
import grpc
from acestep.inference import (
    GenerationParams,
    GenerationConfig,
    generate_music,
    create_sample,
    format_sample,
)
from acestep.handler import AceStepHandler
from acestep.llm_inference import LLMHandler
from acestep.model_downloader import ensure_lm_model


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))

# Model name -> HuggingFace/ModelScope repo (from upstream api_server.py)
MODEL_REPO_MAPPING = {
    "acestep-v15-turbo": "ACE-Step/Ace-Step1.5",
    "acestep-5Hz-lm-0.6B": "ACE-Step/Ace-Step1.5",
    "acestep-5Hz-lm-1.7B": "ACE-Step/Ace-Step1.5",
    "vae": "ACE-Step/Ace-Step1.5",
    "Qwen3-Embedding-0.6B": "ACE-Step/Ace-Step1.5",
    "acestep-v15-base": "ACE-Step/acestep-v15-base",
    "acestep-v15-sft": "ACE-Step/acestep-v15-sft",
    "acestep-v15-turbo-shift3": "ACE-Step/acestep-v15-turbo-shift3",
    "acestep-5Hz-lm-4B": "ACE-Step/acestep-5Hz-lm-4B",
}
DEFAULT_REPO_ID = "ACE-Step/Ace-Step1.5"

def _is_float(s):
    try:
        float(s)
        return True
    except (ValueError, TypeError):
        return False


def _is_int(s):
    try:
        int(s)
        return True
    except (ValueError, TypeError):
        return False


def _parse_timesteps(s):
    if s is None or (isinstance(s, str) and not s.strip()):
        return None
    if isinstance(s, (list, tuple)):
        return [float(x) for x in s]
    try:
        return [float(x.strip()) for x in str(s).split(",") if x.strip()]
    except (ValueError, TypeError):
        return None


def _parse_options(opts_list):
    """Parse repeated 'key:value' options into a dict. Coerce numeric and bool."""
    out = {}
    for opt in opts_list or []:
        if ":" not in opt:
            continue
        key, value = opt.split(":", 1)
        key = key.strip()
        value = value.strip()
        if _is_int(value):
            out[key] = int(value)
        elif _is_float(value):
            out[key] = float(value)
        elif value.lower() in ("true", "false"):
            out[key] = value.lower() == "true"
        else:
            out[key] = value
    return out


def _generate_audio_sync(servicer, payload, dst_path):
    """
    Run full ACE-Step pipeline using acestep.inference:
    - If sample_mode/sample_query: create_sample() for caption/lyrics/metadata.
    - If use_format and caption/lyrics: format_sample().
    - Build GenerationParams and GenerationConfig, then generate_music().
    Writes the first generated audio to dst_path. Raises on failure.
    """

    opts = servicer.options
    dit_handler = servicer.dit_handler
    llm_handler = servicer.llm_handler

    for key, value in opts.items():
        if key not in payload:
            payload[key] = value

    def _opt(name, default):
        return opts.get(name, default)

    lm_temperature = _opt("temperature", 0.85)
    lm_cfg_scale = _opt("lm_cfg_scale", _opt("cfg_scale", 2.0))
    lm_top_k = opts.get("top_k")
    lm_top_p = _opt("top_p", 0.9)
    if lm_top_p is not None and lm_top_p >= 1.0:
        lm_top_p = None
    inference_steps = _opt("inference_steps", 8)
    guidance_scale = _opt("guidance_scale", 7.0)
    batch_size = max(1, int(_opt("batch_size", 1)))

    use_simple = bool(payload.get("sample_query") or payload.get("text"))
    sample_mode = use_simple and (payload.get("thinking") or payload.get("sample_mode"))
    sample_query = (payload.get("sample_query") or payload.get("text") or "").strip()
    use_format = bool(payload.get("use_format"))
    caption = (payload.get("prompt") or payload.get("caption") or "").strip()
    lyrics = (payload.get("lyrics") or "").strip()
    vocal_language = (payload.get("vocal_language") or "en").strip()
    instrumental = bool(payload.get("instrumental"))
    bpm = payload.get("bpm")
    key_scale = (payload.get("key_scale") or "").strip()
    time_signature = (payload.get("time_signature") or "").strip()
    audio_duration = payload.get("audio_duration")
    if audio_duration is not None:
        try:
            audio_duration = float(audio_duration)
        except (TypeError, ValueError):
            audio_duration = None

    if sample_mode and llm_handler and getattr(llm_handler, "llm_initialized", False):
        parsed_language = None
        if sample_query:
            for hint in ("english", "en", "chinese", "zh", "japanese", "ja"):
                if hint in sample_query.lower():
                    parsed_language = "en" if hint == "english" or hint == "en" else hint
                    break
        vocal_lang = vocal_language if vocal_language and vocal_language != "unknown" else parsed_language
        sample_result = create_sample(
            llm_handler=llm_handler,
            query=sample_query or "NO USER INPUT",
            instrumental=instrumental,
            vocal_language=vocal_lang,
            temperature=lm_temperature,
            top_k=lm_top_k,
            top_p=lm_top_p,
            use_constrained_decoding=True,
        )
        if not sample_result.success:
            raise RuntimeError(f"create_sample failed: {sample_result.error or sample_result.status_message}")
        caption = sample_result.caption or caption
        lyrics = sample_result.lyrics or lyrics
        bpm = sample_result.bpm
        key_scale = sample_result.keyscale or key_scale
        time_signature = sample_result.timesignature or time_signature
        if sample_result.duration is not None:
            audio_duration = sample_result.duration
        if getattr(sample_result, "language", None):
            vocal_language = sample_result.language

    if use_format and (caption or lyrics) and llm_handler and getattr(llm_handler, "llm_initialized", False):
        user_metadata = {}
        if bpm is not None:
            user_metadata["bpm"] = bpm
        if audio_duration is not None and float(audio_duration) > 0:
            user_metadata["duration"] = int(audio_duration)
        if key_scale:
            user_metadata["keyscale"] = key_scale
        if time_signature:
            user_metadata["timesignature"] = time_signature
        if vocal_language and vocal_language != "unknown":
            user_metadata["language"] = vocal_language
        format_result = format_sample(
            llm_handler=llm_handler,
            caption=caption,
            lyrics=lyrics,
            user_metadata=user_metadata if user_metadata else None,
            temperature=lm_temperature,
            top_k=lm_top_k,
            top_p=lm_top_p,
            use_constrained_decoding=True,
        )
        if format_result.success:
            caption = format_result.caption or caption
            lyrics = format_result.lyrics or lyrics
            if format_result.duration is not None:
                audio_duration = format_result.duration
            if format_result.bpm is not None:
                bpm = format_result.bpm
            if format_result.keyscale:
                key_scale = format_result.keyscale
            if format_result.timesignature:
                time_signature = format_result.timesignature
            if getattr(format_result, "language", None):
                vocal_language = format_result.language

    thinking = bool(payload.get("thinking"))
    use_cot_metas = not sample_mode
    params = GenerationParams(
        task_type=payload.get("task_type", "text2music"),
        instruction=payload.get("instruction", "Fill the audio semantic mask based on the given conditions:"),
        reference_audio=payload.get("reference_audio_path"),
        src_audio=payload.get("src_audio_path"),
        audio_codes=payload.get("audio_code_string", ""),
        caption=caption,
        lyrics=lyrics,
        instrumental=instrumental or (not lyrics or str(lyrics).strip().lower() in ("[inst]", "[instrumental]")),
        vocal_language=vocal_language or "unknown",
        bpm=bpm,
        keyscale=key_scale,
        timesignature=time_signature,
        duration=float(audio_duration) if audio_duration and float(audio_duration) > 0 else -1.0,
        inference_steps=inference_steps,
        seed=int(payload.get("seed", -1)),
        guidance_scale=guidance_scale,
        use_adg=bool(payload.get("use_adg")),
        cfg_interval_start=float(payload.get("cfg_interval_start", 0.0)),
        cfg_interval_end=float(payload.get("cfg_interval_end", 1.0)),
        shift=float(payload.get("shift", 1.0)),
        infer_method=(payload.get("infer_method") or "ode").strip(),
        timesteps=_parse_timesteps(payload.get("timesteps")),
        repainting_start=float(payload.get("repainting_start", 0.0)),
        repainting_end=float(payload.get("repainting_end", -1)) if payload.get("repainting_end") is not None else -1,
        audio_cover_strength=float(payload.get("audio_cover_strength", 1.0)),
        thinking=thinking,
        lm_temperature=lm_temperature,
        lm_cfg_scale=lm_cfg_scale,
        lm_top_k=lm_top_k or 0,
        lm_top_p=lm_top_p if lm_top_p is not None and lm_top_p < 1.0 else 0.9,
        lm_negative_prompt=payload.get("lm_negative_prompt", "NO USER INPUT"),
        use_cot_metas=use_cot_metas,
        use_cot_caption=bool(payload.get("use_cot_caption", True)),
        use_cot_language=bool(payload.get("use_cot_language", True)),
        use_constrained_decoding=True,
    )

    config = GenerationConfig(
        batch_size=batch_size,
        allow_lm_batch=bool(payload.get("allow_lm_batch", False)),
        use_random_seed=bool(payload.get("use_random_seed", True)),
        seeds=payload.get("seeds"),
        lm_batch_chunk_size=max(1, int(payload.get("lm_batch_chunk_size", 8))),
        constrained_decoding_debug=bool(payload.get("constrained_decoding_debug")),
        audio_format=(payload.get("audio_format") or "flac").strip() or "flac",
    )

    save_dir = tempfile.mkdtemp(prefix="ace_step_")
    try:
        result = generate_music(
            dit_handler=dit_handler,
            llm_handler=llm_handler if (llm_handler and getattr(llm_handler, "llm_initialized", False)) else None,
            params=params,
            config=config,
            save_dir=save_dir,
            progress=None,
        )
        if not result.success:
            raise RuntimeError(result.error or result.status_message or "generate_music failed")

        audios = result.audios or []
        if not audios:
            raise RuntimeError("generate_music returned no audio")

        first_path = audios[0].get("path") or ""
        if not first_path or not os.path.isfile(first_path):
            raise RuntimeError("first generated audio path missing or not a file")

        shutil.copy2(first_path, dst_path)
    finally:
        try:
            shutil.rmtree(save_dir, ignore_errors=True)
        except Exception:
            pass


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.model_path = None
        self.model_dir = None
        self.checkpoint_dir = None
        self.project_root = None
        self.options = {}
        self.dit_handler = None
        self.llm_handler = None

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        try:
            self.options = _parse_options(list(getattr(request, "Options", []) or []))
            model_path = getattr(request, "ModelPath", None) or ""
            model_name = (request.Model or "").strip()
            model_file = (getattr(request, "ModelFile", None) or "").strip()

            # Model dir: where we store checkpoints (always under LocalAI models path, never backend dir)
            if model_path and model_name:
                model_dir = os.path.join(model_path, model_name)
            elif model_file:
                model_dir = model_file
            else:
                model_dir = os.path.abspath(model_name or ".")
            self.model_dir = model_dir
            self.checkpoint_dir = os.path.join(model_dir, "checkpoints")
            self.project_root = model_dir
            self.model_path = os.path.join(self.checkpoint_dir, model_name or os.path.basename(model_dir.rstrip("/\\")))

            config_path = model_name or os.path.basename(model_dir.rstrip("/\\"))
            os.makedirs(self.checkpoint_dir, exist_ok=True)

            self.dit_handler = AceStepHandler()
            # Patch handler so it uses our model dir instead of site-packages/checkpoints
            self.dit_handler._get_project_root = lambda: self.project_root
            device = self.options.get("device", "auto")
            use_flash = self.options.get("use_flash_attention", True)
            if isinstance(use_flash, str):
                use_flash = str(use_flash).lower() in ("1", "true", "yes")
            offload = self.options.get("offload_to_cpu", False)
            if isinstance(offload, str):
                offload = str(offload).lower() in ("1", "true", "yes")
            status_msg, ok = self.dit_handler.initialize_service(
                project_root=self.project_root,
                config_path=config_path,
                device=device,
                use_flash_attention=use_flash,
                compile_model=False,
                offload_to_cpu=offload,
                offload_dit_to_cpu=bool(self.options.get("offload_dit_to_cpu", False)),
            )
            if not ok:
                return backend_pb2.Result(success=False, message=f"DiT init failed: {status_msg}")

            self.llm_handler = None
            if self.options.get("init_lm", True):
                lm_model = self.options.get("lm_model_path", "acestep-5Hz-lm-0.6B")
                
                # Ensure LM model is downloaded before initializing
                try:
                    from pathlib import Path
                    lm_success, lm_msg = ensure_lm_model(
                        model_name=lm_model,
                        checkpoints_dir=Path(self.checkpoint_dir),
                        prefer_source=None,  # Auto-detect HuggingFace vs ModelScope
                    )
                    if not lm_success:
                        print(f"[ace-step] Warning: LM model download failed: {lm_msg}", file=sys.stderr)
                        # Continue anyway - LLM initialization will fail gracefully
                    else:
                        print(f"[ace-step] LM model ready: {lm_msg}", file=sys.stderr)
                except Exception as e:
                    print(f"[ace-step] Warning: LM model download check failed: {e}", file=sys.stderr)
                    # Continue anyway - LLM initialization will fail gracefully
                
                self.llm_handler = LLMHandler()
                lm_backend = (self.options.get("lm_backend") or "vllm").strip().lower()
                if lm_backend not in ("vllm", "pt"):
                    lm_backend = "vllm"
                lm_status, lm_ok = self.llm_handler.initialize(
                    checkpoint_dir=self.checkpoint_dir,
                    lm_model_path=lm_model,
                    backend=lm_backend,
                    device=device,
                    offload_to_cpu=offload,
                    dtype=getattr(self.dit_handler, "dtype", None),
                )
                if not lm_ok:
                    self.llm_handler = None
                    print(f"[ace-step] LM init failed (optional): {lm_status}", file=sys.stderr)

            print(f"[ace-step] LoadModel: model={self.model_path}, options={list(self.options.keys())}", file=sys.stderr)
            return backend_pb2.Result(success=True, message="Model loaded successfully")
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"LoadModel error: {err}")

    def SoundGeneration(self, request, context):
        if not request.dst:
            return backend_pb2.Result(success=False, message="request.dst is required")

        use_simple = bool(request.text)
        if use_simple:
            payload = {
                "sample_query": request.text or "",
                "sample_mode": True,
                "thinking": True,
                "vocal_language": request.language or request.GetLanguage() or "en",
                "instrumental": request.instrumental if request.HasField("instrumental") else False,
            }
        else:
            caption = request.caption or request.GetCaption() or request.text
            payload = {
                "prompt": caption,
                "lyrics": request.lyrics or request.lyrics or "",
                "thinking": request.think if request.HasField("think") else False,
                "vocal_language": request.language or request.GetLanguage() or "en",
            }
            if request.HasField("bpm"):
                payload["bpm"] = request.bpm
            if request.HasField("keyscale") and request.keyscale:
                payload["key_scale"] = request.keyscale
            if request.HasField("timesignature") and request.timesignature:
                payload["time_signature"] = request.timesignature
            if request.HasField("duration") and request.duration:
                payload["audio_duration"] = int(request.duration) if request.duration else None
            if request.src:
                payload["src_audio_path"] = request.src

        _generate_audio_sync(self, payload, request.dst)
        return backend_pb2.Result(success=True, message="Sound generated successfully")

    def TTS(self, request, context):
        if not request.dst:
            return backend_pb2.Result(success=False, message="request.dst is required")
        payload = {
            "sample_query": request.text,
            "sample_mode": True,
            "thinking": False,
            "vocal_language": (request.language if request.language else "") or "en",
            "instrumental": False,
        }
        _generate_audio_sync(self, payload, request.dst)
        return backend_pb2.Result(success=True, message="TTS (music fallback) generated successfully")


def serve(address):
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
    print(f"[ace-step] Server listening on {address}", file=sys.stderr)

    def shutdown(sig, frame):
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)

    try:
        while True:
            import time
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--addr", default="localhost:50051", help="Listen address")
    args = parser.parse_args()
    serve(args.addr)
