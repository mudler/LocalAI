#!/usr/bin/env python3
"""
LocalAI vLLM-Omni Backend

This backend provides gRPC access to vllm-omni for multimodal generation:
- Image generation (text-to-image, image editing)
- Video generation (text-to-video, image-to-video)
- Text generation with multimodal inputs (LLM)
- Text-to-speech generation
"""
from concurrent import futures
import traceback
import argparse
import signal
import sys
import time
import os
import base64
import io

from PIL import Image
import torch
import numpy as np
import soundfile as sf

import backend_pb2
import backend_pb2_grpc

import grpc

from vllm_omni.entrypoints.omni import Omni
from vllm_omni.outputs import OmniRequestOutput
from vllm_omni.diffusion.data import DiffusionParallelConfig
from vllm_omni.utils.platform_utils import detect_device_type, is_npu
from vllm import SamplingParams
from diffusers.utils import export_to_video

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def is_float(s):
    """Check if a string can be converted to float."""
    try:
        float(s)
        return True
    except ValueError:
        return False


def is_int(s):
    """Check if a string can be converted to int."""
    try:
        int(s)
        return True
    except ValueError:
        return False


# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):

    def _detect_model_type(self, model_name):
        """Detect model type from model name."""
        model_lower = model_name.lower()
        if "tts" in model_lower or "qwen3-tts" in model_lower:
            return "tts"
        elif "omni" in model_lower and "qwen3" in model_lower:
            return "llm"
        elif "wan" in model_lower or "t2v" in model_lower or "i2v" in model_lower:
            return "video"
        elif "image" in model_lower or "z-image" in model_lower or "qwen-image" in model_lower:
            return "image"
        else:
            # Default to image for diffusion models, llm for others
            return "image"

    def _detect_tts_task_type(self):
        """Detect TTS task type from model name."""
        model_lower = self.model_name.lower()
        if "customvoice" in model_lower:
            return "CustomVoice"
        elif "voicedesign" in model_lower:
            return "VoiceDesign"
        elif "base" in model_lower:
            return "Base"
        else:
            # Default to CustomVoice
            return "CustomVoice"

    def _load_image(self, image_path):
        """Load an image from file path or base64 encoded data."""
        # Try file path first
        if os.path.exists(image_path):
            return Image.open(image_path)
        # Try base64 decode
        try:
            image_data = base64.b64decode(image_path)
            return Image.open(io.BytesIO(image_data))
        except:
            return None

    def _load_video(self, video_path):
        """Load a video from file path or base64 encoded data."""
        from vllm.assets.video import VideoAsset, video_to_ndarrays
        if os.path.exists(video_path):
            return video_to_ndarrays(video_path, num_frames=16)
        # Try base64 decode
        try:
            timestamp = str(int(time.time() * 1000))
            p = f"/tmp/vl-{timestamp}.data"
            with open(p, "wb") as f:
                f.write(base64.b64decode(video_path))
            video = VideoAsset(name=p).np_ndarrays
            os.remove(p)
            return video
        except:
            return None

    def _load_audio(self, audio_path):
        """Load audio from file path or base64 encoded data."""
        import librosa
        if os.path.exists(audio_path):
            audio_signal, sr = librosa.load(audio_path, sr=16000)
            return (audio_signal.astype(np.float32), sr)
        # Try base64 decode
        try:
            audio_data = base64.b64decode(audio_path)
            # Save to temp file and load
            timestamp = str(int(time.time() * 1000))
            p = f"/tmp/audio-{timestamp}.wav"
            with open(p, "wb") as f:
                f.write(audio_data)
            audio_signal, sr = librosa.load(p, sr=16000)
            os.remove(p)
            return (audio_signal.astype(np.float32), sr)
        except:
            return None

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        try:
            print(f"Loading model {request.Model}...", file=sys.stderr)
            print(f"Request {request}", file=sys.stderr)

            # Parse options from request.Options (key:value pairs)
            self.options = {}
            for opt in request.Options:
                if ":" not in opt:
                    continue
                key, value = opt.split(":", 1)
                # Convert value to appropriate type
                if is_float(value):
                    value = float(value)
                elif is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                self.options[key] = value

            print(f"Options: {self.options}", file=sys.stderr)

            # Detect model type
            self.model_name = request.Model
            self.model_type = request.Type if request.Type else self._detect_model_type(request.Model)
            print(f"Detected model type: {self.model_type}", file=sys.stderr)

            # Build DiffusionParallelConfig if diffusion model (image or video)
            parallel_config = None
            if self.model_type in ["image", "video"]:
                parallel_config = DiffusionParallelConfig(
                    ulysses_degree=self.options.get("ulysses_degree", 1),
                    ring_degree=self.options.get("ring_degree", 1),
                    cfg_parallel_size=self.options.get("cfg_parallel_size", 1),
                    tensor_parallel_size=self.options.get("tensor_parallel_size", 1),
                )

            # Build cache_config dict if cache_backend specified
            cache_backend = self.options.get("cache_backend")  # "cache_dit" or "tea_cache"
            cache_config = None
            if cache_backend == "cache_dit":
                cache_config = {
                    "Fn_compute_blocks": self.options.get("cache_dit_fn_compute_blocks", 1),
                    "Bn_compute_blocks": self.options.get("cache_dit_bn_compute_blocks", 0),
                    "max_warmup_steps": self.options.get("cache_dit_max_warmup_steps", 4),
                    "residual_diff_threshold": self.options.get("cache_dit_residual_diff_threshold", 0.24),
                    "max_continuous_cached_steps": self.options.get("cache_dit_max_continuous_cached_steps", 3),
                    "enable_taylorseer": self.options.get("cache_dit_enable_taylorseer", False),
                    "taylorseer_order": self.options.get("cache_dit_taylorseer_order", 1),
                    "scm_steps_mask_policy": self.options.get("cache_dit_scm_steps_mask_policy"),
                    "scm_steps_policy": self.options.get("cache_dit_scm_steps_policy", "dynamic"),
                }
            elif cache_backend == "tea_cache":
                cache_config = {
                    "rel_l1_thresh": self.options.get("tea_cache_rel_l1_thresh", 0.2),
                }

            # Base Omni initialization parameters
            omni_kwargs = {
                "model": request.Model,
            }

            # Add diffusion-specific parameters (image/video models)
            if self.model_type in ["image", "video"]:
                omni_kwargs.update({
                    "vae_use_slicing": is_npu(),
                    "vae_use_tiling": is_npu(),
                    "cache_backend": cache_backend,
                    "cache_config": cache_config,
                    "parallel_config": parallel_config,
                    "enforce_eager": self.options.get("enforce_eager", request.EnforceEager),
                    "enable_cpu_offload": self.options.get("enable_cpu_offload", False),
                })
                # Video-specific parameters
                if self.model_type == "video":
                    omni_kwargs.update({
                        "boundary_ratio": self.options.get("boundary_ratio", 0.875),
                        "flow_shift": self.options.get("flow_shift", 5.0),
                    })

            # Add LLM/TTS-specific parameters
            if self.model_type in ["llm", "tts"]:
                omni_kwargs.update({
                    "stage_configs_path": self.options.get("stage_configs_path"),
                    "log_stats": self.options.get("enable_stats", False),
                    "stage_init_timeout": self.options.get("stage_init_timeout", 300),
                })
                # vllm engine options (passed through Omni for LLM/TTS)
                if request.GPUMemoryUtilization > 0:
                    omni_kwargs["gpu_memory_utilization"] = request.GPUMemoryUtilization
                if request.TensorParallelSize > 0:
                    omni_kwargs["tensor_parallel_size"] = request.TensorParallelSize
                if request.TrustRemoteCode:
                    omni_kwargs["trust_remote_code"] = request.TrustRemoteCode
                if request.MaxModelLen > 0:
                    omni_kwargs["max_model_len"] = request.MaxModelLen

            self.omni = Omni(**omni_kwargs)
            print("Model loaded successfully", file=sys.stderr)
            return backend_pb2.Result(message="Model loaded successfully", success=True)

        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

    def GenerateImage(self, request, context):
        try:
            # Validate model is loaded and is image/diffusion type
            if not hasattr(self, 'omni'):
                return backend_pb2.Result(success=False, message="Model not loaded. Call LoadModel first.")
            if self.model_type not in ["image"]:
                return backend_pb2.Result(success=False, message=f"Model type {self.model_type} does not support image generation")

            # Extract parameters
            prompt = request.positive_prompt
            negative_prompt = request.negative_prompt if request.negative_prompt else None
            width = request.width if request.width > 0 else 1024
            height = request.height if request.height > 0 else 1024
            seed = request.seed if request.seed > 0 else None
            num_inference_steps = request.step if request.step > 0 else 50
            cfg_scale = self.options.get("cfg_scale", 4.0)
            guidance_scale = self.options.get("guidance_scale", 1.0)

            # Create generator if seed provided
            generator = None
            if seed:
                device = detect_device_type()
                generator = torch.Generator(device=device).manual_seed(seed)

            # Handle image input for image editing
            pil_image = None
            if request.src or (request.ref_images and len(request.ref_images) > 0):
                image_path = request.ref_images[0] if request.ref_images else request.src
                pil_image = self._load_image(image_path)
                if pil_image is None:
                    return backend_pb2.Result(success=False, message=f"Invalid image source: {image_path}")
                pil_image = pil_image.convert("RGB")

            # Build generate kwargs
            generate_kwargs = {
                "prompt": prompt,
                "negative_prompt": negative_prompt,
                "height": height,
                "width": width,
                "generator": generator,
                "true_cfg_scale": cfg_scale,
                "guidance_scale": guidance_scale,
                "num_inference_steps": num_inference_steps,
            }
            if pil_image:
                generate_kwargs["pil_image"] = pil_image

            # Call omni.generate()
            outputs = self.omni.generate(**generate_kwargs)

            # Extract images (following example pattern)
            if not outputs or len(outputs) == 0:
                return backend_pb2.Result(success=False, message="No output generated")

            first_output = outputs[0]
            if not hasattr(first_output, "request_output") or not first_output.request_output:
                return backend_pb2.Result(success=False, message="Invalid output structure")

            req_out = first_output.request_output[0]
            if not isinstance(req_out, OmniRequestOutput) or not hasattr(req_out, "images"):
                return backend_pb2.Result(success=False, message="No images in output")

            images = req_out.images
            if not images or len(images) == 0:
                return backend_pb2.Result(success=False, message="Empty images list")

            # Save image
            output_image = images[0]
            output_image.save(request.dst)
            return backend_pb2.Result(message="Image generated successfully", success=True)

        except Exception as err:
            print(f"Error generating image: {err}", file=sys.stderr)
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"Error generating image: {err}")

    def GenerateVideo(self, request, context):
        try:
            # Validate model is loaded and is video/diffusion type
            if not hasattr(self, 'omni'):
                return backend_pb2.Result(success=False, message="Model not loaded. Call LoadModel first.")
            if self.model_type not in ["video"]:
                return backend_pb2.Result(success=False, message=f"Model type {self.model_type} does not support video generation")

            # Extract parameters
            prompt = request.prompt
            negative_prompt = request.negative_prompt if request.negative_prompt else ""
            width = request.width if request.width > 0 else 1280
            height = request.height if request.height > 0 else 720
            num_frames = request.num_frames if request.num_frames > 0 else 81
            fps = request.fps if request.fps > 0 else 24
            seed = request.seed if request.seed > 0 else None
            guidance_scale = request.cfg_scale if request.cfg_scale > 0 else 4.0
            guidance_scale_high = self.options.get("guidance_scale_high")
            num_inference_steps = request.step if request.step > 0 else 40

            # Create generator
            generator = None
            if seed:
                device = detect_device_type()
                generator = torch.Generator(device=device).manual_seed(seed)

            # Handle image input for image-to-video
            pil_image = None
            if request.start_image:
                pil_image = self._load_image(request.start_image)
                if pil_image is None:
                    return backend_pb2.Result(success=False, message=f"Invalid start_image: {request.start_image}")
                pil_image = pil_image.convert("RGB")
                # Resize to target dimensions
                pil_image = pil_image.resize((width, height), Image.Resampling.LANCZOS)

            # Build generate kwargs
            generate_kwargs = {
                "prompt": prompt,
                "negative_prompt": negative_prompt,
                "height": height,
                "width": width,
                "generator": generator,
                "guidance_scale": guidance_scale,
                "num_inference_steps": num_inference_steps,
                "num_frames": num_frames,
            }
            if pil_image:
                generate_kwargs["pil_image"] = pil_image
            if guidance_scale_high:
                generate_kwargs["guidance_scale_2"] = guidance_scale_high

            # Call omni.generate()
            frames = self.omni.generate(**generate_kwargs)

            # Extract video frames (following example pattern)
            if isinstance(frames, list) and len(frames) > 0:
                first_item = frames[0]

                if hasattr(first_item, "final_output_type"):
                    if first_item.final_output_type != "image":
                        return backend_pb2.Result(success=False, message=f"Unexpected output type: {first_item.final_output_type}")

                    # Pipeline mode: extract from nested request_output
                    if hasattr(first_item, "is_pipeline_output") and first_item.is_pipeline_output:
                        if isinstance(first_item.request_output, list) and len(first_item.request_output) > 0:
                            inner_output = first_item.request_output[0]
                            if isinstance(inner_output, OmniRequestOutput) and hasattr(inner_output, "images"):
                                frames = inner_output.images[0] if inner_output.images else None
                    # Diffusion mode: use direct images field
                    elif hasattr(first_item, "images") and first_item.images:
                        frames = first_item.images
                    else:
                        return backend_pb2.Result(success=False, message="No video frames found")

            if frames is None:
                return backend_pb2.Result(success=False, message="No video frames found in output")

            # Convert frames to numpy array (following example)
            if isinstance(frames, torch.Tensor):
                video_tensor = frames.detach().cpu()
                # Handle different tensor shapes [B, C, F, H, W] or [B, F, H, W, C]
                if video_tensor.dim() == 5:
                    if video_tensor.shape[1] in (3, 4):
                        video_tensor = video_tensor[0].permute(1, 2, 3, 0)
                    else:
                        video_tensor = video_tensor[0]
                elif video_tensor.dim() == 4 and video_tensor.shape[0] in (3, 4):
                    video_tensor = video_tensor.permute(1, 2, 3, 0)
                # Normalize from [-1,1] to [0,1] if float
                if video_tensor.is_floating_point():
                    video_tensor = video_tensor.clamp(-1, 1) * 0.5 + 0.5
                video_array = video_tensor.float().numpy()
            else:
                video_array = frames
                if hasattr(video_array, "shape") and video_array.ndim == 5:
                    video_array = video_array[0]

            # Convert 4D array (frames, H, W, C) to list of frames
            if isinstance(video_array, np.ndarray) and video_array.ndim == 4:
                video_array = list(video_array)

            # Save video
            export_to_video(video_array, request.dst, fps=fps)
            return backend_pb2.Result(message="Video generated successfully", success=True)

        except Exception as err:
            print(f"Error generating video: {err}", file=sys.stderr)
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"Error generating video: {err}")

    def Predict(self, request, context):
        """Non-streaming text generation with multimodal inputs."""
        gen = self._predict(request, context, streaming=False)
        try:
            res = next(gen)
            return res
        except StopIteration:
            return backend_pb2.Reply(message=bytes("", 'utf-8'))

    def PredictStream(self, request, context):
        """Streaming text generation with multimodal inputs."""
        return self._predict(request, context, streaming=True)

    def _predict(self, request, context, streaming=False):
        """Internal method for text generation (streaming and non-streaming)."""
        try:
            # Validate model is loaded and is LLM type
            if not hasattr(self, 'omni'):
                yield backend_pb2.Reply(message=bytes("Model not loaded. Call LoadModel first.", 'utf-8'))
                return
            if self.model_type not in ["llm"]:
                yield backend_pb2.Reply(message=bytes(f"Model type {self.model_type} does not support text generation", 'utf-8'))
                return

            # Extract prompt
            if request.Prompt:
                prompt = request.Prompt
            elif request.Messages and request.UseTokenizerTemplate:
                # Build prompt from messages (simplified - would need tokenizer for full template)
                prompt = ""
                for msg in request.Messages:
                    role = msg.role
                    content = msg.content
                    prompt += f"<|im_start|>{role}\n{content}<|im_end|>\n"
                prompt += "<|im_start|>assistant\n"
            else:
                yield backend_pb2.Reply(message=bytes("", 'utf-8'))
                return

            # Build multi_modal_data dict
            multi_modal_data = {}

            # Process images
            if request.Images:
                image_data = []
                for img_path in request.Images:
                    img = self._load_image(img_path)
                    if img:
                        # Convert to format expected by vllm
                        from vllm.multimodal.image import convert_image_mode
                        img_data = convert_image_mode(img, "RGB")
                        image_data.append(img_data)
                if image_data:
                    multi_modal_data["image"] = image_data

            # Process videos
            if request.Videos:
                video_data = []
                for video_path in request.Videos:
                    video = self._load_video(video_path)
                    if video is not None:
                        video_data.append(video)
                if video_data:
                    multi_modal_data["video"] = video_data

            # Process audio
            if request.Audios:
                audio_data = []
                for audio_path in request.Audios:
                    audio = self._load_audio(audio_path)
                    if audio is not None:
                        audio_data.append(audio)
                if audio_data:
                    multi_modal_data["audio"] = audio_data

            # Build inputs dict
            inputs = {
                "prompt": prompt,
                "multi_modal_data": multi_modal_data if multi_modal_data else None,
            }

            # Build sampling params
            sampling_params = SamplingParams(
                temperature=request.Temperature if request.Temperature > 0 else 0.7,
                top_p=request.TopP if request.TopP > 0 else 0.9,
                top_k=request.TopK if request.TopK > 0 else -1,
                max_tokens=request.Tokens if request.Tokens > 0 else 200,
                presence_penalty=request.PresencePenalty if request.PresencePenalty != 0 else 0.0,
                frequency_penalty=request.FrequencyPenalty if request.FrequencyPenalty != 0 else 0.0,
                repetition_penalty=request.RepetitionPenalty if request.RepetitionPenalty != 0 else 1.0,
                seed=request.Seed if request.Seed > 0 else None,
                stop=request.StopPrompts if request.StopPrompts else None,
                stop_token_ids=request.StopTokenIds if request.StopTokenIds else None,
                ignore_eos=request.IgnoreEOS,
            )
            sampling_params_list = [sampling_params]

            # Call omni.generate() (returns generator for LLM mode)
            omni_generator = self.omni.generate([inputs], sampling_params_list)

            # Extract text from outputs
            generated_text = ""
            for stage_outputs in omni_generator:
                if stage_outputs.final_output_type == "text":
                    for output in stage_outputs.request_output:
                        text_output = output.outputs[0].text
                        if streaming:
                            # Remove already sent text (vllm concatenates)
                            delta_text = text_output.removeprefix(generated_text)
                            yield backend_pb2.Reply(message=bytes(delta_text, encoding='utf-8'))
                        generated_text = text_output

            if not streaming:
                yield backend_pb2.Reply(message=bytes(generated_text, encoding='utf-8'))

        except Exception as err:
            print(f"Error in Predict: {err}", file=sys.stderr)
            traceback.print_exc()
            yield backend_pb2.Reply(message=bytes(f"Error: {err}", encoding='utf-8'))

    def TTS(self, request, context):
        try:
            # Validate model is loaded and is TTS type
            if not hasattr(self, 'omni'):
                return backend_pb2.Result(success=False, message="Model not loaded. Call LoadModel first.")
            if self.model_type not in ["tts"]:
                return backend_pb2.Result(success=False, message=f"Model type {self.model_type} does not support TTS")

            # Extract parameters
            text = request.text
            language = request.language if request.language else "Auto"
            voice = request.voice if request.voice else None
            task_type = self._detect_tts_task_type()

            # Build prompt with chat template
            # TODO: for now vllm-omni supports only qwen3-tts, so we hardcode it, however, we want to support other models in the future.
            # and we might need to use the chat template here
            prompt = f"<|im_start|>assistant\n{text}<|im_end|>\n<|im_start|>assistant\n"

            # Build inputs dict
            inputs = {
                "prompt": prompt,
                "additional_information": {
                    "task_type": [task_type],
                    "text": [text],
                    "language": [language],
                    "max_new_tokens": [2048],
                }
            }

            # Add task-specific fields
            if task_type == "CustomVoice":
                if voice:
                    inputs["additional_information"]["speaker"] = [voice]
                # Add instruct if provided in options
                if "instruct" in self.options:
                    inputs["additional_information"]["instruct"] = [self.options["instruct"]]
            elif task_type == "VoiceDesign":
                if "instruct" in self.options:
                    inputs["additional_information"]["instruct"] = [self.options["instruct"]]
                inputs["additional_information"]["non_streaming_mode"] = [True]
            elif task_type == "Base":
                # Voice cloning requires ref_audio and ref_text
                if "ref_audio" in self.options:
                    inputs["additional_information"]["ref_audio"] = [self.options["ref_audio"]]
                if "ref_text" in self.options:
                    inputs["additional_information"]["ref_text"] = [self.options["ref_text"]]
                if "x_vector_only_mode" in self.options:
                    inputs["additional_information"]["x_vector_only_mode"] = [self.options["x_vector_only_mode"]]

            # Build sampling params
            sampling_params = SamplingParams(
                temperature=0.9,
                top_p=1.0,
                top_k=50,
                max_tokens=2048,
                seed=42,
                detokenize=False,
                repetition_penalty=1.05,
            )
            sampling_params_list = [sampling_params]

            # Call omni.generate()
            omni_generator = self.omni.generate(inputs, sampling_params_list)

            # Extract audio (following TTS example)
            for stage_outputs in omni_generator:
                for output in stage_outputs.request_output:
                    if "audio" in output.multimodal_output:
                        audio_tensor = output.multimodal_output["audio"]
                        audio_samplerate = output.multimodal_output["sr"].item()

                        # Convert to numpy
                        audio_numpy = audio_tensor.float().detach().cpu().numpy()
                        if audio_numpy.ndim > 1:
                            audio_numpy = audio_numpy.flatten()

                        # Save audio file
                        sf.write(request.dst, audio_numpy, samplerate=audio_samplerate, format="WAV")
                        return backend_pb2.Result(message="TTS audio generated successfully", success=True)

            return backend_pb2.Result(success=False, message="No audio output generated")

        except Exception as err:
            print(f"Error generating TTS: {err}", file=sys.stderr)
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"Error generating TTS: {err}")


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    # Signal handlers for graceful shutdown
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
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    serve(args.addr)
