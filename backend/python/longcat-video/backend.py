#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import datetime
import gc
import math
import os
import signal
import subprocess
import sys
import tempfile
import traceback
from concurrent import futures

import grpc

import backend_pb2
import backend_pb2_grpc

from longcat_utils import (
    BASE_MODEL_ID,
    MODEL_KIND_AVATAR,
    MODEL_KIND_BASE,
    attention_overrides,
    avatar_segments_for_duration,
    avatar_segments_for_frames,
    classify_model,
    normalize_model_source,
    normalize_num_frames,
    parse_options,
    require_bool,
    require_float,
    require_int,
    validate_dimensions,
)

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "sources", "LongCat-Video"))

from grpc_auth import get_auth_interceptors


MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))

DEFAULT_NEGATIVE_PROMPT = (
    "Close-up, bright tones, overexposed, static, blurred details, subtitles, "
    "paintings, low quality, JPEG compression residue, ugly, incomplete, extra "
    "fingers, poorly drawn hands, poorly drawn faces, deformed, disfigured, "
    "misshapen limbs, fused fingers, still picture, messy background, three legs, "
    "many people in the background, walking backwards"
)

LOAD_OPTIONS = {
    "attention_backend",
    "base_model",
    "max_segments",
    "resolution",
    "use_distill",
    "use_int8",
}

REQUEST_PARAMS = {
    "audio_guidance_scale",
    "mask_frame_range",
    "num_segments",
    "offload_kv_cache",
    "ref_img_index",
    "resolution",
}

BASE_CHECKPOINT_PATTERNS = [
    "config.json",
    "model_index.json",
    "dit/**",
    "lora/cfg_step_lora.safetensors",
    "scheduler/**",
    "text_encoder/**",
    "tokenizer/**",
    "vae/**",
]

AVATAR_BASE_PATTERNS = [
    "config.json",
    "model_index.json",
    "text_encoder/**",
    "tokenizer/**",
    "vae/**",
]

AVATAR_COMMON_PATTERNS = [
    "config.json",
    "lora/dmd_lora.safetensors",
    "model_index.json",
    "scheduler/**",
    "whisper-large-v3/config.json",
    "whisper-large-v3/model.safetensors",
    "whisper-large-v3/preprocessor_config.json",
]


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.model_kind = None
        self.pipeline = None
        self.options = {}
        self.device_index = 0
        self.cp_split_hw = None
        self._dist_store_dir = None

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        model = request.Model
        if request.ModelFile and os.path.isdir(request.ModelFile):
            model = request.ModelFile

        model_kind = classify_model(model)
        if model_kind is None:
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "longcat-video only accepts LongCat-Video or LongCat-Video-Avatar-1.5 checkpoints",
            )

        try:
            options = parse_options(request.Options)
            unknown = sorted(set(options) - LOAD_OPTIONS)
            if unknown:
                raise ValueError(f"unknown model option(s): {', '.join(unknown)}")

            self._import_torch()
            if not self.torch.cuda.is_available():
                return self._fail(
                    context,
                    grpc.StatusCode.FAILED_PRECONDITION,
                    "longcat-video requires an NVIDIA CUDA GPU",
                )
            if request.TensorParallelSize > 1:
                return self._fail(
                    context,
                    grpc.StatusCode.UNIMPLEMENTED,
                    "longcat-video currently supports one GPU per backend process",
                )
            self._import_runtime()

            attention_name = str(options.get("attention_backend", "sdpa")).lower()
            attention_overrides(attention_name)
            resolution = str(options.get("resolution", "480p")).lower()
            if resolution not in {"480p", "720p"}:
                raise ValueError("resolution must be 480p or 720p")

            use_distill_default = model_kind == MODEL_KIND_AVATAR
            use_distill = require_bool(
                options.get("use_distill", use_distill_default),
                "use_distill",
            )
            use_int8 = require_bool(options.get("use_int8", False), "use_int8")
            if model_kind == MODEL_KIND_BASE and use_int8:
                raise ValueError(
                    "use_int8 is supported only by LongCat-Video-Avatar-1.5"
                )

            self.options = {
                **options,
                "attention_backend": attention_name,
                "resolution": resolution,
                "use_distill": use_distill,
                "use_int8": use_int8,
                "max_segments": require_int(
                    options.get("max_segments", 8),
                    "max_segments",
                    minimum=1,
                    maximum=64,
                ),
            }

            self._release_model()
            self._ensure_distributed()
            if model_kind == MODEL_KIND_BASE:
                self._load_base_model(model)
            else:
                self._load_avatar_model(model)
            self.model_kind = model_kind
            print(
                f"Loaded {normalize_model_source(model)} as {model_kind} "
                f"with attention_backend={attention_name}",
                file=sys.stderr,
            )
            return backend_pb2.Result(message="Model loaded successfully", success=True)
        except ValueError as err:
            self._release_model()
            return self._fail(context, grpc.StatusCode.INVALID_ARGUMENT, str(err))
        except Exception as err:
            self._release_model()
            print(f"Error loading LongCat model: {err}", file=sys.stderr)
            traceback.print_exc()
            return self._fail(
                context,
                grpc.StatusCode.INTERNAL,
                f"failed to load LongCat model: {err}",
            )

    def Free(self, request, context):
        self._release_model()
        return backend_pb2.Result(message="Model released", success=True)

    def GenerateVideo(self, request, context):
        if self.pipeline is None or self.model_kind is None:
            return self._fail(
                context,
                grpc.StatusCode.FAILED_PRECONDITION,
                "model is not loaded",
            )
        if not request.prompt.strip():
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "prompt is required",
            )
        if not request.dst:
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "output destination is required",
            )
        if request.end_image:
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "longcat-video does not support end_image conditioning",
            )

        request_state = {"finished": False}

        def interrupt_if_cancelled():
            if not request_state["finished"] and self.pipeline is not None:
                self.pipeline._interrupt = True

        try:
            params = dict(request.params)
            unknown = sorted(set(params) - REQUEST_PARAMS)
            if unknown:
                raise ValueError(f"unknown request param(s): {', '.join(unknown)}")

            os.makedirs(os.path.dirname(request.dst) or ".", mode=0o750, exist_ok=True)
            if hasattr(context, "add_callback"):
                context.add_callback(interrupt_if_cancelled)

            if request.start_image and not os.path.isfile(request.start_image):
                raise ValueError("start_image is not a readable staged file")
            if request.num_frames < 0:
                raise ValueError("num_frames must not be negative")

            if self.model_kind == MODEL_KIND_BASE:
                if request.audio:
                    raise ValueError(
                        "audio input requires a LongCat-Video-Avatar-1.5 model"
                    )
                self._generate_base(request, params)
            else:
                self._generate_avatar(request, params, context)

            return backend_pb2.Result(
                message="Video generated successfully", success=True
            )
        except ValueError as err:
            return self._fail(context, grpc.StatusCode.INVALID_ARGUMENT, str(err))
        except Exception as err:
            print(f"Error generating LongCat video: {err}", file=sys.stderr)
            traceback.print_exc()
            return self._fail(
                context,
                grpc.StatusCode.INTERNAL,
                f"LongCat video generation failed: {err}",
            )
        finally:
            request_state["finished"] = True
            if self.pipeline is not None:
                self.pipeline._interrupt = False

    def _import_torch(self):
        if hasattr(self, "torch"):
            return

        import torch

        self.torch = torch

    def _import_runtime(self):
        if hasattr(self, "LongCatVideoPipeline"):
            return

        import imageio.v2 as imageio
        import imageio_ffmpeg
        import librosa
        import numpy as np
        import torch.distributed as dist
        from diffusers.utils import load_image
        from huggingface_hub import snapshot_download
        from PIL import Image
        from transformers import AutoTokenizer, UMT5EncoderModel

        from longcat_video.audio_process import (
            get_audio_encoder,
            get_audio_feature_extractor,
        )
        from longcat_video.context_parallel import context_parallel_util
        from longcat_video.modules.autoencoder_kl_wan import AutoencoderKLWan
        from longcat_video.modules.avatar.longcat_video_dit_avatar import (
            LongCatVideoAvatarTransformer3DModel,
        )
        from longcat_video.modules.longcat_video_dit import (
            LongCatVideoTransformer3DModel,
        )
        from longcat_video.modules.quantization import load_quantized_dit
        from longcat_video.modules.scheduling_flow_match_euler_discrete import (
            FlowMatchEulerDiscreteScheduler,
        )
        from longcat_video.pipeline_longcat_video import LongCatVideoPipeline
        from longcat_video.pipeline_longcat_video_avatar import (
            LongCatVideoAvatarPipeline,
        )

        self.imageio = imageio
        self.imageio_ffmpeg = imageio_ffmpeg
        self.librosa = librosa
        self.np = np
        self.dist = dist
        self.load_image = load_image
        self.snapshot_download = snapshot_download
        self.Image = Image
        self.AutoTokenizer = AutoTokenizer
        self.UMT5EncoderModel = UMT5EncoderModel
        self.get_audio_encoder = get_audio_encoder
        self.get_audio_feature_extractor = get_audio_feature_extractor
        self.context_parallel_util = context_parallel_util
        self.AutoencoderKLWan = AutoencoderKLWan
        self.LongCatVideoAvatarTransformer3DModel = LongCatVideoAvatarTransformer3DModel
        self.LongCatVideoTransformer3DModel = LongCatVideoTransformer3DModel
        self.load_quantized_dit = load_quantized_dit
        self.FlowMatchEulerDiscreteScheduler = FlowMatchEulerDiscreteScheduler
        self.LongCatVideoPipeline = LongCatVideoPipeline
        self.LongCatVideoAvatarPipeline = LongCatVideoAvatarPipeline

    def _ensure_distributed(self):
        self.torch.cuda.set_device(self.device_index)
        if not self.dist.is_initialized():
            self._dist_store_dir = tempfile.mkdtemp(prefix="localai-longcat-dist-")
            init_file = os.path.join(self._dist_store_dir, "store")
            self.dist.init_process_group(
                backend="nccl",
                init_method=f"file://{init_file}",
                rank=0,
                world_size=1,
                timeout=datetime.timedelta(hours=24),
            )
            self.context_parallel_util.init_context_parallel(
                context_parallel_size=1,
                global_rank=0,
                world_size=1,
            )
            self.cp_split_hw = self.context_parallel_util.get_optimal_split(1)

    def _resolve_checkpoint(self, model, patterns):
        source = normalize_model_source(model)
        if os.path.isdir(source):
            return source
        print(f"Downloading required files for {source}", file=sys.stderr)
        return self.snapshot_download(repo_id=source, allow_patterns=patterns)

    def _load_base_model(self, model):
        checkpoint = self._resolve_checkpoint(model, BASE_CHECKPOINT_PATTERNS)
        dtype = self.torch.bfloat16
        overrides = attention_overrides(self.options["attention_backend"])

        tokenizer = self.AutoTokenizer.from_pretrained(
            checkpoint,
            subfolder="tokenizer",
        )
        text_encoder = self.UMT5EncoderModel.from_pretrained(
            checkpoint,
            subfolder="text_encoder",
            torch_dtype=dtype,
            low_cpu_mem_usage=True,
        )
        vae = self.AutoencoderKLWan.from_pretrained(
            checkpoint,
            subfolder="vae",
            torch_dtype=dtype,
            low_cpu_mem_usage=True,
        )
        scheduler = self.FlowMatchEulerDiscreteScheduler.from_pretrained(
            checkpoint,
            subfolder="scheduler",
        )
        dit = self.LongCatVideoTransformer3DModel.from_pretrained(
            checkpoint,
            subfolder="dit",
            cp_split_hw=self.cp_split_hw,
            torch_dtype=dtype,
            low_cpu_mem_usage=True,
            **overrides,
        )
        if self.options["use_distill"]:
            dit.load_lora(
                os.path.join(checkpoint, "lora", "cfg_step_lora.safetensors"),
                "cfg_step_lora",
            )
            dit.enable_loras(["cfg_step_lora"])

        self.pipeline = self.LongCatVideoPipeline(
            tokenizer=tokenizer,
            text_encoder=text_encoder,
            vae=vae,
            scheduler=scheduler,
            dit=dit,
        )
        self.pipeline.to(self.device_index)

    def _load_avatar_model(self, model):
        avatar_patterns = list(AVATAR_COMMON_PATTERNS)
        model_subfolder = (
            "base_model_int8" if self.options["use_int8"] else "base_model"
        )
        avatar_patterns.append(f"{model_subfolder}/**")
        checkpoint = self._resolve_checkpoint(model, avatar_patterns)

        base_model = self.options.get("base_model")
        if not base_model and os.path.isdir(normalize_model_source(model)):
            sibling = os.path.join(
                os.path.dirname(normalize_model_source(model)), "LongCat-Video"
            )
            if os.path.isdir(sibling):
                base_model = sibling
        base_model = base_model or BASE_MODEL_ID
        if classify_model(str(base_model)) != MODEL_KIND_BASE:
            raise ValueError("base_model must point to a LongCat-Video checkpoint")
        base_checkpoint = self._resolve_checkpoint(base_model, AVATAR_BASE_PATTERNS)

        dtype = self.torch.bfloat16
        overrides = attention_overrides(self.options["attention_backend"])
        tokenizer = self.AutoTokenizer.from_pretrained(
            base_checkpoint,
            subfolder="tokenizer",
        )
        text_encoder = self.UMT5EncoderModel.from_pretrained(
            base_checkpoint,
            subfolder="text_encoder",
            torch_dtype=dtype,
            low_cpu_mem_usage=True,
        )
        vae = self.AutoencoderKLWan.from_pretrained(
            base_checkpoint,
            subfolder="vae",
            torch_dtype=dtype,
            low_cpu_mem_usage=True,
        )
        scheduler = self.FlowMatchEulerDiscreteScheduler.from_pretrained(
            checkpoint,
            subfolder="scheduler",
        )

        if self.options["use_int8"]:
            previous_dtype = self.torch.get_default_dtype()
            self.torch.set_default_dtype(dtype)
            try:
                dit = self.load_quantized_dit(
                    checkpoint,
                    subfolder="base_model_int8",
                    cp_split_hw=self.cp_split_hw,
                    **overrides,
                )
            finally:
                self.torch.set_default_dtype(previous_dtype)
        else:
            dit = self.LongCatVideoAvatarTransformer3DModel.from_pretrained(
                checkpoint,
                subfolder="base_model",
                cp_split_hw=self.cp_split_hw,
                torch_dtype=dtype,
                low_cpu_mem_usage=True,
                **overrides,
            )

        if self.options["use_distill"]:
            dit.load_lora(
                os.path.join(checkpoint, "lora", "dmd_lora.safetensors"),
                "dmd",
                multiplier=1.0,
                lora_network_dim=128,
                lora_network_alpha=64,
            )
            dit.enable_loras(["dmd"])

        audio_checkpoint = os.path.join(checkpoint, "whisper-large-v3")
        audio_encoder = self.get_audio_encoder(
            audio_checkpoint,
            MODEL_KIND_AVATAR + "-v1.5",
        ).to(self.device_index)
        audio_feature_extractor = self.get_audio_feature_extractor(
            audio_checkpoint,
            MODEL_KIND_AVATAR + "-v1.5",
        )
        self.pipeline = self.LongCatVideoAvatarPipeline(
            tokenizer=tokenizer,
            text_encoder=text_encoder,
            vae=vae,
            scheduler=scheduler,
            dit=dit,
            audio_encoder=audio_encoder,
            audio_feature_extractor=audio_feature_extractor,
            model_type="avatar-v1.5",
        )
        self.pipeline.to(self.device_index)

    def _generate_base(self, request, params):
        use_distill = self.options["use_distill"]
        frames = normalize_num_frames(request.num_frames)
        steps = (
            16
            if use_distill
            else require_int(
                request.step or 50,
                "step",
                minimum=1,
                maximum=200,
            )
        )
        guidance_scale = (
            1.0
            if use_distill
            else require_float(
                request.cfg_scale or 4.0,
                "cfg_scale",
                minimum=0.0,
                maximum=30.0,
            )
        )
        fps = require_int(request.fps or 15, "fps", minimum=1, maximum=60)
        seed = request.seed if request.seed > 0 else 42
        negative_prompt = request.negative_prompt or DEFAULT_NEGATIVE_PROMPT
        generator = self.torch.Generator(device=self.device_index).manual_seed(seed)

        if request.start_image:
            resolution = self._resolution(params)
            image = self.load_image(request.start_image)
            output = self.pipeline.generate_i2v(
                image=image,
                prompt=request.prompt,
                negative_prompt=negative_prompt,
                resolution=resolution,
                num_frames=frames,
                num_inference_steps=steps,
                use_distill=use_distill,
                guidance_scale=guidance_scale,
                generator=generator,
            )[0]
        else:
            width, height = validate_dimensions(request.width, request.height)
            output = self.pipeline.generate_t2v(
                prompt=request.prompt,
                negative_prompt=negative_prompt,
                height=height,
                width=width,
                num_frames=frames,
                num_inference_steps=steps,
                use_distill=use_distill,
                guidance_scale=guidance_scale,
                generator=generator,
            )[0]

        self._save_video(output, request.dst, fps)

    def _generate_avatar(self, request, params, context):
        if not request.audio:
            raise ValueError("audio is required for LongCat-Video-Avatar-1.5")
        if not os.path.isfile(request.audio):
            raise ValueError("audio input is not a readable staged file")

        use_distill = self.options["use_distill"]
        steps = (
            8
            if use_distill
            else require_int(
                request.step or 50,
                "step",
                minimum=1,
                maximum=200,
            )
        )
        text_guidance = (
            1.0
            if use_distill
            else require_float(
                request.cfg_scale or 4.0,
                "cfg_scale",
                minimum=0.0,
                maximum=30.0,
            )
        )
        audio_guidance = (
            1.0
            if use_distill
            else require_float(
                params.get("audio_guidance_scale", 4.0),
                "audio_guidance_scale",
                minimum=0.0,
                maximum=20.0,
            )
        )
        seed = request.seed if request.seed > 0 else 42
        generator = self.torch.Generator(device=self.device_index).manual_seed(seed)
        negative_prompt = request.negative_prompt or DEFAULT_NEGATIVE_PROMPT
        resolution = self._resolution(params)

        speech, sample_rate = self.librosa.load(request.audio, sr=16000, mono=True)
        if speech.size == 0:
            raise ValueError("audio contains no samples")
        audio_duration = len(speech) / sample_rate
        segments = self._avatar_segments(request, params, audio_duration)

        segment_frames = 93
        conditioning_frames = 13
        avatar_fps = 25
        generated_duration = (
            segment_frames + (segments - 1) * (segment_frames - conditioning_frames)
        ) / avatar_fps
        pad_samples = max(
            0, math.ceil((generated_duration - audio_duration) * sample_rate)
        )
        if pad_samples:
            speech = self.np.pad(speech, (0, pad_samples))

        full_audio_embedding = self.pipeline.get_audio_embedding(
            speech,
            fps=avatar_fps,
            device=self.device_index,
            sample_rate=sample_rate,
            model_type="avatar-v1.5",
        )
        if not self.torch.isfinite(full_audio_embedding).all():
            raise ValueError("audio encoder returned non-finite values")

        indices = self.torch.arange(5) - 2

        def audio_window(start_index):
            centers = self.torch.arange(
                start_index,
                start_index + segment_frames,
            ).unsqueeze(1) + indices.unsqueeze(0)
            centers = self.torch.clamp(
                centers,
                min=0,
                max=full_audio_embedding.shape[0] - 1,
            )
            return full_audio_embedding[centers][None, ...].to(self.device_index)

        audio_start = 0
        common = {
            "prompt": request.prompt,
            "negative_prompt": negative_prompt,
            "num_frames": segment_frames,
            "num_inference_steps": steps,
            "text_guidance_scale": text_guidance,
            "audio_guidance_scale": audio_guidance,
            "output_type": "both",
            "generator": generator,
            "audio_emb": audio_window(audio_start),
            "use_distill": use_distill,
        }

        if request.start_image:
            output, latent = self.pipeline.generate_ai2v(
                image=self.load_image(request.start_image),
                resolution=resolution,
                **common,
            )
        else:
            width, height = validate_dimensions(request.width, request.height)
            output, latent = self.pipeline.generate_at2v(
                height=height,
                width=width,
                **common,
            )

        video = self._frames_to_pil(output[0])
        width, height = video[0].size
        current_video = video
        reference_latent = latent[:, :, :1].clone()
        all_frames = list(video)

        for segment in range(1, segments):
            if hasattr(context, "is_active") and not context.is_active():
                raise RuntimeError("request was cancelled")
            print(
                f"Generating avatar segment {segment + 1}/{segments}", file=sys.stderr
            )
            audio_start += segment_frames - conditioning_frames
            output, latent = self.pipeline.generate_avc(
                video=current_video,
                video_latent=latent,
                prompt=request.prompt,
                negative_prompt=negative_prompt,
                height=height,
                width=width,
                num_frames=segment_frames,
                num_cond_frames=conditioning_frames,
                num_inference_steps=steps,
                text_guidance_scale=text_guidance,
                audio_guidance_scale=audio_guidance,
                generator=generator,
                output_type="both",
                use_kv_cache=True,
                offload_kv_cache=require_bool(
                    params.get("offload_kv_cache", False),
                    "offload_kv_cache",
                ),
                enhance_hf=not use_distill,
                audio_emb=audio_window(audio_start),
                ref_latent=reference_latent,
                ref_img_index=require_int(
                    params.get("ref_img_index", 10),
                    "ref_img_index",
                    minimum=-30,
                    maximum=30,
                ),
                mask_frame_range=require_int(
                    params.get("mask_frame_range", 3),
                    "mask_frame_range",
                    minimum=0,
                    maximum=32,
                ),
                use_distill=use_distill,
            )
            current_video = self._frames_to_pil(output[0])
            all_frames.extend(current_video[conditioning_frames:])

        self._save_avatar_video(all_frames, request.audio, request.dst, avatar_fps)

    def _avatar_segments(self, request, params, audio_duration):
        if "num_segments" in params:
            segments = require_int(
                params["num_segments"],
                "num_segments",
                minimum=1,
            )
        elif request.num_frames > 0:
            segments = avatar_segments_for_frames(request.num_frames)
        else:
            segments = avatar_segments_for_duration(audio_duration)

        max_segments = self.options["max_segments"]
        if segments > max_segments:
            raise ValueError(
                f"request needs {segments} avatar segments, but max_segments is {max_segments}; "
                "trim the audio or raise the model's max_segments option"
            )
        return segments

    def _resolution(self, params):
        resolution = str(params.get("resolution", self.options["resolution"])).lower()
        if resolution not in {"480p", "720p"}:
            raise ValueError("resolution must be 480p or 720p")
        return resolution

    def _frames_to_pil(self, frames):
        images = []
        for frame in frames:
            array = self.np.asarray(frame)
            if self.np.issubdtype(array.dtype, self.np.floating):
                array = self.np.clip(array, 0.0, 1.0) * 255
            images.append(self.Image.fromarray(array.astype(self.np.uint8)))
        return images

    def _save_video(self, frames, path, fps):
        writer = self.imageio.get_writer(
            path,
            format="FFMPEG",
            mode="I",
            fps=fps,
            codec="libx264",
            macro_block_size=1,
            ffmpeg_params=[
                "-crf",
                "18",
                "-pix_fmt",
                "yuv420p",
                "-movflags",
                "+faststart",
                "-f",
                "mp4",
            ],
        )
        try:
            for frame in frames:
                array = self.np.asarray(frame)
                if self.np.issubdtype(array.dtype, self.np.floating):
                    array = self.np.clip(array, 0.0, 1.0) * 255
                writer.append_data(array.astype(self.np.uint8))
        finally:
            writer.close()

    def _save_avatar_video(self, frames, audio_path, dst, fps):
        output_dir = os.path.dirname(dst) or "."
        handle, silent_path = tempfile.mkstemp(
            prefix="longcat-silent-",
            suffix=".mp4",
            dir=output_dir,
        )
        os.close(handle)
        try:
            self._save_video(frames, silent_path, fps)
            command = [
                self.imageio_ffmpeg.get_ffmpeg_exe(),
                "-y",
                "-i",
                silent_path,
                "-i",
                audio_path,
                "-map",
                "0:v:0",
                "-map",
                "1:a:0",
                "-c:v",
                "copy",
                "-c:a",
                "aac",
                "-b:a",
                "192k",
                "-shortest",
                "-movflags",
                "+faststart",
                "-f",
                "mp4",
                dst,
            ]
            subprocess.run(
                command,
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
        except subprocess.CalledProcessError as err:
            details = (err.stderr or "ffmpeg failed")[-2000:]
            raise RuntimeError(f"failed to mux avatar audio: {details}") from err
        finally:
            try:
                os.remove(silent_path)
            except FileNotFoundError:
                pass

    def _release_model(self):
        self.pipeline = None
        self.model_kind = None
        gc.collect()
        if hasattr(self, "torch") and self.torch.cuda.is_available():
            self.torch.cuda.empty_cache()
            self.torch.cuda.ipc_collect()

    @staticmethod
    def _fail(context, code, message):
        if context is not None:
            context.set_code(code)
            context.set_details(message)
        return backend_pb2.Result(message=message, success=False)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 64 * 1024 * 1024),
            ("grpc.max_send_message_length", 64 * 1024 * 1024),
            ("grpc.max_receive_message_length", 64 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print(f"LongCat Video backend listening on {address}", file=sys.stderr)

    def stop_server(signum, frame):
        del signum, frame
        server.stop(0)

    signal.signal(signal.SIGINT, stop_server)
    signal.signal(signal.SIGTERM, stop_server)
    server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the LongCat Video gRPC backend")
    parser.add_argument(
        "--addr",
        default="localhost:50051",
        help="address on which to serve the backend",
    )
    arguments = parser.parse_args()
    serve(arguments.addr)
