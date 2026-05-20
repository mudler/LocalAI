#!/usr/bin/env python3
"""
Liquid Audio backend for LocalAI.

Wraps LiquidAI's `liquid-audio` Python package (https://github.com/Liquid4All/liquid-audio).
The same model serves four roles, selected by the `mode` option at load time:
chat, asr, tts, s2s. Fine-tuning is exposed via StartFineTune.
"""
from concurrent import futures
import argparse
import json
import os
import queue
import signal
import sys
import threading
import time
import traceback
import uuid

import grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors  # noqa: E402
from python_utils import parse_options  # noqa: E402

import backend_pb2  # noqa: E402
import backend_pb2_grpc  # noqa: E402

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Voice id → system-prompt suffix. The model only ships these four voices.
VOICE_PROMPTS = {
    "us_male":   "Perform TTS. Use the US male voice.",
    "us_female": "Perform TTS. Use the US female voice.",
    "uk_male":   "Perform TTS. Use the UK male voice.",
    "uk_female": "Perform TTS. Use the UK female voice.",
}
DEFAULT_VOICE = "us_female"

# Special-token IDs that LFM2-Audio emits to delimit modality boundaries.
# Sourced from liquid_audio/model/lfm2_audio.py (see generate_sequential/_sample_*).
TEXT_END_TOKEN = 130        # <|text_end|>
AUDIO_START_TOKEN = 128     # <|audio_start|>
IM_END_TOKEN = 7            # <|im_end|>
AUDIO_EOS_CODE = 2048       # signals end-of-audio in any codebook position

_PATCHED_LOCAL_PATHS = False


def _patch_liquid_audio_local_paths():
    """Make liquid_audio.utils.get_model_dir() tolerate local directories.

    Upstream always passes its argument to huggingface_hub.snapshot_download,
    which only accepts `owner/repo` ids. LocalAI's gallery hands us absolute
    paths under <ModelPath>/<owner>/<repo>, so we intercept snapshot_download
    in the liquid_audio.utils namespace and return the directory as-is when
    it already exists on disk. Idempotent.
    """
    global _PATCHED_LOCAL_PATHS
    if _PATCHED_LOCAL_PATHS:
        return
    import liquid_audio.utils as _la_utils
    _orig_snapshot_download = _la_utils.snapshot_download

    def _local_first_snapshot_download(repo_id, revision=None, **kwargs):
        if isinstance(repo_id, (str, os.PathLike)) and os.path.isdir(str(repo_id)):
            return str(repo_id)
        return _orig_snapshot_download(repo_id, revision=revision, **kwargs)

    _la_utils.snapshot_download = _local_first_snapshot_download
    _PATCHED_LOCAL_PATHS = True


def _select_device():
    import torch
    if torch.cuda.is_available():
        return "cuda"
    if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
        return "mps"
    return "cpu"


class ActiveJob:
    """Tracks an in-flight fine-tune so FineTuneProgress can stream from its queue."""

    def __init__(self, job_id):
        self.job_id = job_id
        self.progress_queue = queue.Queue()
        self.thread = None
        self.stopped = False
        self.completed = False
        self.error = None


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.processor = None
        self.model = None
        self.device = "cpu"
        self.dtype = None
        self.options = {}
        self.model_id = None
        self.active_job = None

    @property
    def mode(self):
        return str(self.options.get("mode", "chat")).lower()

    @property
    def voice(self):
        v = str(self.options.get("voice", DEFAULT_VOICE)).lower()
        return v if v in VOICE_PROMPTS else DEFAULT_VOICE


    def Free(self, request, context):
        # Called by LocalAI when unloading the model. Drop GPU tensors so the
        # next load starts from a clean state instead of bumping into OOM.
        try:
            for attr in ("model", "processor", "tokenizer"):
                if hasattr(self, attr):
                    try:
                        delattr(self, attr)
                    except Exception:
                        pass
            import gc
            gc.collect()
            try:
                import torch
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()
            except Exception:
                pass
            return backend_pb2.Result(success=True, message="OK")
        except Exception as exc:
            print(f"Free failed: {exc}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(exc))


    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))


    def LoadModel(self, request, context):
        try:
            import torch

            self.options = parse_options(request.Options)
            if self.options.get("voice") and self.options["voice"] not in VOICE_PROMPTS:
                print(f"Warning: unknown voice '{self.options['voice']}'; defaulting to '{DEFAULT_VOICE}'",
                      file=sys.stderr)

            requested_device = self.options.get("device")
            self.device = requested_device or _select_device()
            if self.device == "cuda" and not torch.cuda.is_available():
                return backend_pb2.Result(success=False, message="CUDA requested but not available")
            if self.device == "mps" and not (hasattr(torch.backends, "mps") and
                                             torch.backends.mps.is_available()):
                print("MPS not available; falling back to CPU", file=sys.stderr)
                self.device = "cpu"

            dtype_name = str(self.options.get("dtype", "bfloat16")).lower()
            self.dtype = {
                "bfloat16": torch.bfloat16,
                "bf16":     torch.bfloat16,
                "float16":  torch.float16,
                "fp16":     torch.float16,
                "half":     torch.float16,
                "float32":  torch.float32,
                "fp32":     torch.float32,
            }.get(dtype_name, torch.bfloat16)

            # request.Model holds the raw `parameters.model` value (an HF
            # repo id like "LiquidAI/LFM2.5-Audio-1.5B"); request.ModelFile
            # is LocalAI's ModelPath-prefixed local copy that exists only
            # when the gallery supplied a `files:` list. Mirror the
            # transformers/vibevoice convention: prefer the repo id and
            # only switch to the local path if it's been staged on disk.
            model_id = request.Model
            if not model_id:
                model_id = request.ModelFile
            if not model_id:
                return backend_pb2.Result(success=False, message="No model identifier provided")
            if request.ModelFile and os.path.isdir(request.ModelFile):
                model_id = request.ModelFile
            self.model_id = model_id

            # Pure fine-tune jobs don't need an in-memory inference model — the
            # Trainer instantiates its own copy at StartFineTune time.
            if self.mode == "finetune":
                print(f"Loaded liquid-audio backend in fine-tune mode (model id: {model_id})",
                      file=sys.stderr)
                return backend_pb2.Result(success=True, message="OK")

            from liquid_audio import LFM2AudioModel, LFM2AudioProcessor

            # liquid_audio's from_pretrained unconditionally routes through
            # huggingface_hub.snapshot_download, which rejects local paths
            # (HFValidationError on `/models/LiquidAI/LFM2.5-Audio-1.5B`).
            # When LocalAI's gallery has already staged the weights on disk,
            # short-circuit the download to return the local directory.
            _patch_liquid_audio_local_paths()

            print(f"Loading liquid-audio model '{model_id}' on {self.device} ({self.dtype})",
                  file=sys.stderr)
            self.processor = LFM2AudioProcessor.from_pretrained(model_id, device=self.device).eval()
            self.model = LFM2AudioModel.from_pretrained(
                model_id, device=self.device, dtype=self.dtype
            ).eval()

            print(f"Liquid-audio mode={self.mode}, voice={self.voice}", file=sys.stderr)
            return backend_pb2.Result(success=True, message="OK")

        except Exception as exc:
            print(f"LoadModel failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(exc))


    def Predict(self, request, context):
        try:
            text = "".join(self._generate_text_stream(request))
            return backend_pb2.Reply(message=text.encode("utf-8"))
        except Exception as exc:
            print(f"Predict failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(exc))
            return backend_pb2.Reply()

    def PredictStream(self, request, context):
        try:
            for delta in self._generate_text_stream(request):
                yield backend_pb2.Reply(message=delta.encode("utf-8"))
        except Exception as exc:
            print(f"PredictStream failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(exc))


    def VAD(self, request, context):
        # Stub voice-activity detector: RMS-energy threshold over 30ms frames at
        # 16 kHz. Good enough for the realtime endpoint's handleVAD loop, which
        # only inspects segment presence + last segment end. The proper signal
        # would come from the model's audio encoder, but that ride-along is a
        # PR-D scope item — until then this keeps the legacy pipeline path
        # working without forcing the operator to install a separate VAD model.
        import numpy as np
        try:
            audio = np.asarray(request.audio, dtype=np.float32)
            if audio.size == 0:
                return backend_pb2.VADResponse(segments=[])

            sample_rate = 16000
            frame_size = sample_rate * 30 // 1000  # 30ms → 480 samples
            threshold = float(self.options.get("vad_rms_threshold", 0.01))
            min_speech_frames = int(self.options.get("vad_min_speech_frames", 2))  # ≥60ms
            # handleVAD ticks every 300 ms and only inspects segment presence
            # + last segment end relative to silence_threshold (~500 ms). Cap
            # the analysed window to the tail of the buffer so we don't redo
            # the entire growing utterance every tick.
            window_s = float(self.options.get("vad_window_s", 5.0))
            window_samples = int(window_s * sample_rate)
            time_offset_s = 0.0
            if audio.size > window_samples:
                time_offset_s = (audio.size - window_samples) / sample_rate
                audio = audio[-window_samples:]

            n_frames = audio.size // frame_size
            if n_frames == 0:
                return backend_pb2.VADResponse(segments=[])
            frames = audio[: n_frames * frame_size].reshape(n_frames, frame_size)
            rms = np.sqrt(np.mean(frames ** 2, axis=1))
            speech = rms > threshold

            def _emit(start_idx, end_idx, out):
                if end_idx - start_idx >= min_speech_frames:
                    out.append(backend_pb2.VADSegment(
                        start=time_offset_s + start_idx * frame_size / sample_rate,
                        end=time_offset_s + end_idx * frame_size / sample_rate,
                    ))

            segments = []
            start_idx = None
            for i, is_speech in enumerate(speech):
                if is_speech and start_idx is None:
                    start_idx = i
                elif not is_speech and start_idx is not None:
                    _emit(start_idx, i, segments)
                    start_idx = None
            if start_idx is not None:
                _emit(start_idx, n_frames, segments)
            return backend_pb2.VADResponse(segments=segments)
        except Exception as exc:
            print(f"VAD failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(exc))
            return backend_pb2.VADResponse(segments=[])


    def TTS(self, request, context):
        try:
            if self.model is None or self.processor is None:
                return backend_pb2.Result(success=False, message="Model not loaded")

            import torch
            import torchaudio
            from liquid_audio import ChatState

            voice = request.voice.lower() if request.voice else self.voice
            voice = voice.removeprefix("lfm2:").removeprefix("lfm:")
            if voice not in VOICE_PROMPTS:
                voice = self.voice
            system_prompt = VOICE_PROMPTS[voice]

            chat = ChatState(self.processor)
            chat.new_turn("system")
            chat.add_text(system_prompt)
            chat.end_turn()
            chat.new_turn("user")
            chat.add_text(request.text or "")
            chat.end_turn()
            chat.new_turn("assistant")

            audio_top_k = int(self.options.get("audio_top_k", 64))
            audio_temp = float(self.options.get("audio_temperature", 0.8))
            max_new = int(self.options.get("max_new_tokens", 2048))

            audio_out = []
            for tok in self.model.generate_sequential(
                **chat,
                max_new_tokens=max_new,
                audio_temperature=audio_temp,
                audio_top_k=audio_top_k,
            ):
                if tok.numel() > 1:
                    audio_out.append(tok)

            if len(audio_out) <= 1:
                return backend_pb2.Result(success=False, message="No audio frames generated")

            # Drop the trailing end-of-audio frame, matching the package's examples.
            audio_codes = torch.stack(audio_out[:-1], 1).unsqueeze(0)
            waveform = self.processor.decode(audio_codes)

            out_path = request.dst
            if not out_path:
                return backend_pb2.Result(success=False, message="dst path is required")
            os.makedirs(os.path.dirname(out_path) or ".", exist_ok=True)
            # soundfile in preference to torchaudio.save — the latter routes
            # through torchcodec, whose native libs need NVIDIA NPP that we
            # don't bundle in the cuda13 image.
            import soundfile as _sf
            _sf.write(out_path, waveform.cpu().numpy().squeeze(0).T, 24_000)

            return backend_pb2.Result(success=True)
        except Exception as exc:
            print(f"TTS failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(exc))


    def AudioToAudioStream(self, request_iterator, context):
        """Bidirectional any-to-any speech-to-speech stream.

        See `backend.proto` AudioToAudioStream for the wire protocol. Audio
        is decoded once per turn here; chunked detokenization for sub-second
        TTFB is left to a future iteration once the LFM2AudioDetokenizer
        gains a streaming entry point.
        """
        try:
            yield from self._audio_to_audio_stream(request_iterator, context)
        except Exception as exc:
            print(f"AudioToAudioStream failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            yield backend_pb2.AudioToAudioResponse(
                event="error",
                meta=json.dumps({"message": str(exc)}).encode("utf-8"),
            )

    def _audio_to_audio_stream(self, request_iterator, context):
        if self.model is None or self.processor is None:
            raise RuntimeError("Model not loaded")

        import torch
        import torchaudio
        from liquid_audio import ChatState

        cfg = None
        chat = None
        input_sample_rate = 16000
        output_sample_rate = 24000
        sequence = 0

        def _new_event(event, **kwargs):
            nonlocal sequence
            sequence += 1
            kwargs.setdefault("sequence", sequence)
            return backend_pb2.AudioToAudioResponse(event=event, **kwargs)

        def _ensure_chat():
            """Build a fresh ChatState seeded with the system prompt."""
            nonlocal chat
            chat = ChatState(self.processor)
            system_prompt = (cfg.system_prompt if cfg and cfg.system_prompt
                             else "Respond with interleaved text and audio.")
            chat.new_turn("system")
            chat.add_text(system_prompt)
            chat.end_turn()

        # Buffers for the in-flight user turn
        pcm_buffer = bytearray()

        def _consume_user_turn():
            nonlocal pcm_buffer
            if not pcm_buffer:
                return
            # Avoid the bytes(pcm_buffer) copy and let the float widen happen
            # in-place: numpy view → torch view → in-place divide.
            import numpy as np
            arr = np.frombuffer(memoryview(pcm_buffer), dtype=np.int16)
            wav = torch.from_numpy(arr).to(torch.float32).div_(32768.0).unsqueeze(0)
            chat.new_turn("user")
            chat.add_audio(wav, input_sample_rate)
            chat.end_turn()
            pcm_buffer = bytearray()

        def _run_generation():
            """Run generate_interleaved; yield response events as we go."""
            chat.new_turn("assistant")
            audio_top_k = int(self.options.get("audio_top_k", 4))
            audio_temp = float(self.options.get("audio_temperature", 1.0))
            text_top_k = int(self.options.get("text_top_k", 0)) or None
            text_temp = float(self.options.get("text_temperature", 0)) or None
            max_new = int(self.options.get("max_new_tokens", 512))

            audio_tokens = []
            for tok in self.model.generate_interleaved(
                **chat,
                max_new_tokens=max_new,
                text_temperature=text_temp,
                text_top_k=text_top_k,
                audio_temperature=audio_temp,
                audio_top_k=audio_top_k,
            ):
                if tok.numel() == 1:
                    if tok.item() == IM_END_TOKEN:
                        break
                    text = self.processor.text.decode(tok)
                    if not text:
                        continue
                    yield _new_event(
                        "response.audio_transcript.delta",
                        meta=json.dumps({"delta": text}).encode("utf-8"),
                    )
                else:
                    audio_tokens.append(tok)

            # Detokenize the accumulated audio at end-of-turn — the
            # LFM2AudioDetokenizer is non-streaming today.
            if len(audio_tokens) > 1:
                audio_codes = torch.stack(audio_tokens[:-1], 1).unsqueeze(0)
                waveform = self.processor.decode(audio_codes)
                # Convert to s16le PCM bytes at output_sample_rate
                if output_sample_rate != 24000:
                    waveform = torchaudio.functional.resample(
                        waveform.cpu(), 24000, output_sample_rate
                    )
                pcm = (waveform.cpu().squeeze(0).clamp(-1, 1) * 32767.0).to(
                    torch.int16
                ).numpy().tobytes()
                yield _new_event(
                    "response.audio.delta",
                    pcm=pcm,
                    sample_rate=output_sample_rate,
                )

            yield _new_event("response.done", meta=b"{}")

        for req in request_iterator:
            if not context.is_active():
                return
            payload = req.WhichOneof("payload")
            if payload == "config":
                cfg = req.config
                if cfg.input_sample_rate > 0:
                    input_sample_rate = cfg.input_sample_rate
                if cfg.output_sample_rate > 0:
                    output_sample_rate = cfg.output_sample_rate
                # The first config implicitly resets state.
                _ensure_chat()
                pcm_buffer = bytearray()
            elif payload == "frame":
                if chat is None:
                    _ensure_chat()
                if req.frame.pcm:
                    pcm_buffer.extend(req.frame.pcm)
                if req.frame.end_of_input:
                    _consume_user_turn()
                    yield from _run_generation()
            elif payload == "control":
                event = req.control.event
                if event == "input_audio_buffer.commit":
                    _consume_user_turn()
                    yield from _run_generation()
                elif event == "response.cancel":
                    # Synchronous generation here means cancel can only
                    # take effect between turns; we ack so the client unblocks.
                    yield _new_event("response.done", meta=b'{"cancelled":true}')
                elif event == "session.update":
                    # Free-form session re-config; treat as a soft reset.
                    _ensure_chat()
                    pcm_buffer = bytearray()
                # Unknown events are ignored — forward-compatible.


    def AudioTranscription(self, request, context):
        try:
            if self.model is None or self.processor is None:
                return backend_pb2.TranscriptResult(segments=[], text="")

            import torchaudio
            from liquid_audio import ChatState

            audio_path = request.dst
            if not audio_path:
                return backend_pb2.TranscriptResult(segments=[], text="")

            chat = ChatState(self.processor)
            chat.new_turn("system")
            chat.add_text("Perform ASR.")
            chat.end_turn()
            chat.new_turn("user")
            # soundfile in preference to torchaudio.load — the latter routes
            # through torchcodec which needs NVIDIA NPP libs we don't bundle.
            import soundfile as _sf
            import torch
            audio_np, sr = _sf.read(audio_path, dtype="float32", always_2d=True)
            wav = torch.from_numpy(audio_np.T)  # (channels, samples)
            if wav.shape[0] > 1:
                # Down-mix to mono — the processor expects a single channel
                wav = wav.mean(dim=0, keepdim=True)
            chat.add_audio(wav, sr)
            chat.end_turn()
            chat.new_turn("assistant")

            max_new = int(self.options.get("max_new_tokens", 1024))

            pieces = []
            for tok in self.model.generate_sequential(**chat, max_new_tokens=max_new):
                if tok.numel() == 1:
                    if tok.item() == IM_END_TOKEN:
                        break
                    pieces.append(self.processor.text.decode(tok))

            text = "".join(pieces).strip()
            duration_ms = int((wav.shape[1] / sr) * 1000)
            segment = backend_pb2.TranscriptSegment(
                id=0, start=0, end=duration_ms, text=text, tokens=[],
            )
            return backend_pb2.TranscriptResult(segments=[segment], text=text)
        except Exception as exc:
            print(f"AudioTranscription failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")


    def StartFineTune(self, request, context):
        if self.active_job is not None and not self.active_job.completed:
            return backend_pb2.FineTuneJobResult(
                job_id="", success=False,
                message="A fine-tuning job is already running",
            )

        job_id = request.job_id or str(uuid.uuid4())
        job = ActiveJob(job_id)
        self.active_job = job

        thread = threading.Thread(target=self._run_training, args=(request, job), daemon=True)
        job.thread = thread
        thread.start()

        return backend_pb2.FineTuneJobResult(
            job_id=job_id, success=True, message="Training started",
        )

    def FineTuneProgress(self, request, context):
        if self.active_job is None or self.active_job.job_id != request.job_id:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details(f"Job {request.job_id} not found")
            return

        job = self.active_job
        while True:
            try:
                update = job.progress_queue.get(timeout=1.0)
            except queue.Empty:
                if job.completed or job.stopped:
                    break
                if not context.is_active():
                    break
                continue
            if update is None:
                break
            yield update
            if update.status in ("completed", "failed", "stopped"):
                break

    def StopFineTune(self, request, context):
        # We can't kill the Accelerate training loop mid-step cleanly from here;
        # LocalAI's job manager kills the backend process on stop. The flag below
        # at least lets the progress stream terminate quickly.
        if self.active_job is not None and self.active_job.job_id == request.job_id:
            self.active_job.stopped = True
            self.active_job.progress_queue.put(None)
        return backend_pb2.Result(success=True, message="OK")

    def _run_training(self, request, job):
        try:
            self._do_train(request, job)
            job.completed = True
            job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
                job_id=job.job_id, status="completed", message="Training completed",
                progress_percent=100.0,
            ))
        except Exception as exc:
            job.error = str(exc)
            job.completed = True
            print(f"Training failed: {exc}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
                job_id=job.job_id, status="failed", message=str(exc),
            ))
        finally:
            job.progress_queue.put(None)

    def _do_train(self, request, job):
        from liquid_audio import LFM2AudioModel  # noqa: F401  (sanity import)
        from liquid_audio.data.dataloader import LFM2DataLoader
        from liquid_audio.trainer import Trainer

        model_id = request.model or self.model_id or "LiquidAI/LFM2.5-Audio-1.5B"

        dataset_path = request.dataset_source
        if not dataset_path:
            raise ValueError("dataset_source is required (path to a preprocessed dataset)")

        extras = dict(request.extra_options) if request.extra_options else {}
        val_path = extras.get("val_dataset")

        # Map FineTuneRequest hyperparameters to liquid_audio.Trainer constructor args
        lr = request.learning_rate or 3e-5
        max_steps = request.max_steps or 1000
        warmup_steps = request.warmup_steps or min(100, max_steps // 10)
        batch_size = request.batch_size or 16
        save_interval = request.save_steps or max(1, max_steps // 4)

        output_dir = request.output_dir or os.path.join(
            os.environ.get("LIQUID_AUDIO_OUTPUT_DIR", "/tmp"),
            f"liquid-audio-{job.job_id}",
        )
        os.makedirs(output_dir, exist_ok=True)

        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="loading_dataset",
            message=f"Loading preprocessed dataset from {dataset_path}",
        ))
        train_data = LFM2DataLoader(dataset_path)
        val_data = LFM2DataLoader(val_path) if val_path else None

        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="loading_model",
            message=f"Loading base model {model_id}",
        ))

        # The Liquid Trainer logs via self.accelerator.print; we subclass it to
        # also push progress events onto the queue every logging_interval steps.
        progress_q = job.progress_queue

        class QueuedTrainer(Trainer):
            def log(self_, model_output):
                if self_.step > 0 and self_.step % self_.logging_interval == 0:
                    try:
                        loss = self_.accelerator.reduce(
                            model_output.loss.detach(), reduction="mean"
                        ).item()
                    except Exception:
                        loss = float("nan")
                    lr_now = self_.optimizer.param_groups[0]["lr"]
                    pct = (self_.step / self_.max_steps * 100.0) if self_.max_steps else 0.0
                    progress_q.put(backend_pb2.FineTuneProgressUpdate(
                        job_id=job.job_id,
                        current_step=int(self_.step),
                        total_steps=int(self_.max_steps),
                        current_epoch=float(self_.epoch),
                        loss=float(loss),
                        learning_rate=float(lr_now),
                        progress_percent=float(pct),
                        status="training",
                    ))
                # Honour stop requests: raising here terminates the loop cleanly
                if job.stopped:
                    raise KeyboardInterrupt("stop requested")
                return super().log(model_output)

            def validate(self_):
                progress_q.put(backend_pb2.FineTuneProgressUpdate(
                    job_id=job.job_id, current_step=int(self_.step),
                    total_steps=int(self_.max_steps), status="training",
                    message=f"Running validation at step {self_.step}",
                ))
                return super().validate()

        trainer = QueuedTrainer(
            model_id=model_id,
            train_data=train_data,
            val_data=val_data,
            lr=lr,
            max_steps=max_steps,
            warmup_steps=warmup_steps,
            batch_size=batch_size,
            save_interval=save_interval,
            output_dir=output_dir,
            weight_decay=request.weight_decay or 0.1,
        )

        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="training", message="Training started",
            total_steps=int(max_steps),
        ))
        trainer.train()

        job.progress_queue.put(backend_pb2.FineTuneProgressUpdate(
            job_id=job.job_id, status="saving",
            message=f"Saved final model to {output_dir}",
            checkpoint_path=os.path.join(output_dir, "final"),
        ))


    def _build_chat_state(self, messages, user_prompt, tools_prelude=None):
        """Build a ChatState from a list of (role, content) tuples plus an optional final user turn.

        tools_prelude, when non-empty, is prepended as an extra system turn carrying
        the LFM2 tool-list block — mirrors gallery/lfm.yaml's `function:` template
        so the model sees the same prompt shape whether served via llama-cpp or here.
        """
        from liquid_audio import ChatState
        chat = ChatState(self.processor)
        if tools_prelude:
            chat.new_turn("system")
            chat.add_text(tools_prelude)
            chat.end_turn()
        for role, content in messages:
            chat.new_turn(role)
            chat.add_text(content)
            chat.end_turn()
        if user_prompt:
            chat.new_turn("user")
            chat.add_text(user_prompt)
            chat.end_turn()
        chat.new_turn("assistant")
        return chat

    def _collect_messages(self, request):
        """Translate PredictOptions.Messages into (role, content) tuples."""
        out = []
        for m in request.Messages:
            role = (m.role or "user").lower()
            if role not in ("system", "user", "assistant"):
                role = "user"
            out.append((role, m.content or ""))
        return out

    def _render_tools_prelude(self, request):
        """Build the LFM2 `<|tool_list_start|>…<|tool_list_end|>` system prelude
        from request.Tools (OpenAI Chat-Completions tool JSON). Returns "" when
        no tools are attached. Output mirrors gallery/lfm.yaml's `function:`
        template so the model sees the same prompt whether routed via llama-cpp
        or this backend."""
        tools_raw = getattr(request, "Tools", "") or ""
        if not tools_raw:
            return ""
        try:
            tools = json.loads(tools_raw)
        except json.JSONDecodeError:
            print(f"liquid-audio: ignoring malformed Tools JSON: {tools_raw[:200]!r}",
                  file=sys.stderr)
            return ""
        if not isinstance(tools, list) or not tools:
            return ""
        # The LFM2 chat template uses single-quoted Python-dict-ish syntax in
        # examples, but the tokenizer treats this whole block as opaque text;
        # JSON works fine and is what other backends emit.
        return (
            "You are a function calling AI model. You are provided with functions to "
            "execute. You may call one or more functions to assist with the user query. "
            "Don't make assumptions about what values to plug into functions.\n"
            "List of tools: <|tool_list_start|>"
            + json.dumps(tools, separators=(",", ":"))
            + "<|tool_list_end|>"
        )

    def _generate_text_stream(self, request):
        """Yield text-only deltas from generate_sequential. Caller joins for unary Predict."""
        if self.model is None or self.processor is None:
            raise RuntimeError("Model not loaded")
        messages = self._collect_messages(request)
        user_prompt = request.Prompt or None
        tools_prelude = self._render_tools_prelude(request)
        # If the request already carries Messages, Prompt is the templated form
        # of the same content — don't append a duplicate user turn.
        chat = self._build_chat_state(
            messages,
            user_prompt if not messages else None,
            tools_prelude=tools_prelude,
        )

        max_new = request.Tokens if request.Tokens > 0 else int(self.options.get("max_new_tokens", 512))
        temperature = request.Temperature if request.Temperature > 0 else None
        top_k = request.TopK if request.TopK > 0 else None

        for tok in self.model.generate_sequential(
            **chat,
            max_new_tokens=max_new,
            text_temperature=temperature,
            text_top_k=top_k,
        ):
            if tok.numel() == 1:
                if tok.item() == IM_END_TOKEN:
                    break
                yield self.processor.text.decode(tok)


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
    print(f"Liquid-audio backend listening on {address}", file=sys.stderr, flush=True)

    def stop(_signum, _frame):
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Liquid Audio gRPC backend")
    parser.add_argument("--addr", default="localhost:50051", help="gRPC server address")
    args = parser.parse_args()
    serve(args.addr)
