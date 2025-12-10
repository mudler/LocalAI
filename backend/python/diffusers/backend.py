#!/usr/bin/env python3
"""
LocalAI Diffusers Backend

This backend provides gRPC access to diffusers pipelines with dynamic pipeline loading.
New pipelines added to diffusers become available automatically without code changes.
"""
from concurrent import futures
import traceback
import argparse
from collections import defaultdict
from enum import Enum
import signal
import sys
import time
import os

from PIL import Image
import torch

import backend_pb2
import backend_pb2_grpc

import grpc

# Import dynamic loader for pipeline discovery
from diffusers_dynamic_loader import (
    get_pipeline_registry,
    resolve_pipeline_class,
    get_available_pipelines,
    load_diffusers_pipeline,
)

# Import specific items still needed for special cases and safety checker
from diffusers import DiffusionPipeline, ControlNetModel
from diffusers import FluxPipeline, FluxTransformer2DModel, AutoencoderKLWan
from diffusers.pipelines.stable_diffusion import safety_checker
from diffusers.utils import load_image, export_to_video
from compel import Compel, ReturnedEmbeddingsType
from optimum.quanto import freeze, qfloat8, quantize
from transformers import T5EncoderModel
from safetensors.torch import load_file

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
COMPEL = os.environ.get("COMPEL", "0") == "1"
XPU = os.environ.get("XPU", "0") == "1"
CLIPSKIP = os.environ.get("CLIPSKIP", "1") == "1"
SAFETENSORS = os.environ.get("SAFETENSORS", "1") == "1"
CHUNK_SIZE = os.environ.get("CHUNK_SIZE", "8")
FPS = os.environ.get("FPS", "7")
DISABLE_CPU_OFFLOAD = os.environ.get("DISABLE_CPU_OFFLOAD", "0") == "1"
FRAMES = os.environ.get("FRAMES", "64")

if XPU:
    print(torch.xpu.get_device_name(0))

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


# https://github.com/CompVis/stable-diffusion/issues/239#issuecomment-1627615287
def sc(self, clip_input, images): return images, [False for i in images]


# edit the StableDiffusionSafetyChecker class so that, when called, it just returns the images and an array of True values
safety_checker.StableDiffusionSafetyChecker.forward = sc

from diffusers.schedulers import (
    DDIMScheduler,
    DPMSolverMultistepScheduler,
    DPMSolverSinglestepScheduler,
    EulerAncestralDiscreteScheduler,
    EulerDiscreteScheduler,
    HeunDiscreteScheduler,
    KDPM2AncestralDiscreteScheduler,
    KDPM2DiscreteScheduler,
    LMSDiscreteScheduler,
    PNDMScheduler,
    UniPCMultistepScheduler,
)

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


# The scheduler list mapping was taken from here: https://github.com/neggles/animatediff-cli/blob/6f336f5f4b5e38e85d7f06f1744ef42d0a45f2a7/src/animatediff/schedulers.py#L39
# Credits to https://github.com/neggles
# See https://github.com/huggingface/diffusers/issues/4167 for more details on sched mapping from A1111
class DiffusionScheduler(str, Enum):
    ddim = "ddim"  # DDIM
    pndm = "pndm"  # PNDM
    heun = "heun"  # Heun
    unipc = "unipc"  # UniPC
    euler = "euler"  # Euler
    euler_a = "euler_a"  # Euler a

    lms = "lms"  # LMS
    k_lms = "k_lms"  # LMS Karras

    dpm_2 = "dpm_2"  # DPM2
    k_dpm_2 = "k_dpm_2"  # DPM2 Karras

    dpm_2_a = "dpm_2_a"  # DPM2 a
    k_dpm_2_a = "k_dpm_2_a"  # DPM2 a Karras

    dpmpp_2m = "dpmpp_2m"  # DPM++ 2M
    k_dpmpp_2m = "k_dpmpp_2m"  # DPM++ 2M Karras

    dpmpp_sde = "dpmpp_sde"  # DPM++ SDE
    k_dpmpp_sde = "k_dpmpp_sde"  # DPM++ SDE Karras

    dpmpp_2m_sde = "dpmpp_2m_sde"  # DPM++ 2M SDE
    k_dpmpp_2m_sde = "k_dpmpp_2m_sde"  # DPM++ 2M SDE Karras


def get_scheduler(name: str, config: dict = {}):
    is_karras = name.startswith("k_")
    if is_karras:
        # strip the k_ prefix and add the karras sigma flag to config
        name = name.lstrip("k_")
        config["use_karras_sigmas"] = True

    if name == DiffusionScheduler.ddim:
        sched_class = DDIMScheduler
    elif name == DiffusionScheduler.pndm:
        sched_class = PNDMScheduler
    elif name == DiffusionScheduler.heun:
        sched_class = HeunDiscreteScheduler
    elif name == DiffusionScheduler.unipc:
        sched_class = UniPCMultistepScheduler
    elif name == DiffusionScheduler.euler:
        sched_class = EulerDiscreteScheduler
    elif name == DiffusionScheduler.euler_a:
        sched_class = EulerAncestralDiscreteScheduler
    elif name == DiffusionScheduler.lms:
        sched_class = LMSDiscreteScheduler
    elif name == DiffusionScheduler.dpm_2:
        # Equivalent to DPM2 in K-Diffusion
        sched_class = KDPM2DiscreteScheduler
    elif name == DiffusionScheduler.dpm_2_a:
        # Equivalent to `DPM2 a`` in K-Diffusion
        sched_class = KDPM2AncestralDiscreteScheduler
    elif name == DiffusionScheduler.dpmpp_2m:
        # Equivalent to `DPM++ 2M` in K-Diffusion
        sched_class = DPMSolverMultistepScheduler
        config["algorithm_type"] = "dpmsolver++"
        config["solver_order"] = 2
    elif name == DiffusionScheduler.dpmpp_sde:
        # Equivalent to `DPM++ SDE` in K-Diffusion
        sched_class = DPMSolverSinglestepScheduler
    elif name == DiffusionScheduler.dpmpp_2m_sde:
        # Equivalent to `DPM++ 2M SDE` in K-Diffusion
        sched_class = DPMSolverMultistepScheduler
        config["algorithm_type"] = "sde-dpmsolver++"
    else:
        raise ValueError(f"Invalid scheduler '{'k_' if is_karras else ''}{name}'")

    return sched_class.from_config(config)


# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):

    def _load_pipeline(self, request, modelFile, fromSingleFile, torchType, variant):
        """
        Load a diffusers pipeline dynamically using the dynamic loader.

        This method uses load_diffusers_pipeline() for most pipelines, falling back
        to explicit handling only for pipelines requiring custom initialization
        (e.g., quantization, special VAE handling).

        Args:
            request: The gRPC request containing pipeline configuration
            modelFile: Path to the model file (for single file loading)
            fromSingleFile: Whether to use from_single_file() vs from_pretrained()
            torchType: The torch dtype to use
            variant: Model variant (e.g., "fp16")

        Returns:
            The loaded pipeline instance
        """
        pipeline_type = request.PipelineType

        # Handle IMG2IMG request flag with default pipeline
        if request.IMG2IMG and pipeline_type == "":
            pipeline_type = "StableDiffusionImg2ImgPipeline"

        # ================================================================
        # Special cases requiring custom initialization logic
        # Only handle pipelines that truly need custom code (quantization,
        # special VAE handling, etc.). All other pipelines use dynamic loading.
        # ================================================================

        # FluxTransformer2DModel - requires quantization and custom transformer loading
        if pipeline_type == "FluxTransformer2DModel":
            dtype = torch.bfloat16
            bfl_repo = os.environ.get("BFL_REPO", "ChuckMcSneed/FLUX.1-dev")

            transformer = FluxTransformer2DModel.from_single_file(modelFile, torch_dtype=dtype)
            quantize(transformer, weights=qfloat8)
            freeze(transformer)
            text_encoder_2 = T5EncoderModel.from_pretrained(bfl_repo, subfolder="text_encoder_2", torch_dtype=dtype)
            quantize(text_encoder_2, weights=qfloat8)
            freeze(text_encoder_2)

            pipe = FluxPipeline.from_pretrained(bfl_repo, transformer=None, text_encoder_2=None, torch_dtype=dtype)
            pipe.transformer = transformer
            pipe.text_encoder_2 = text_encoder_2

            if request.LowVRAM:
                pipe.enable_model_cpu_offload()
            return pipe

        # WanPipeline - requires special VAE with float32 dtype
        if pipeline_type == "WanPipeline":
            vae = AutoencoderKLWan.from_pretrained(
                request.Model,
                subfolder="vae",
                torch_dtype=torch.float32
            )
            pipe = load_diffusers_pipeline(
                class_name="WanPipeline",
                model_id=request.Model,
                vae=vae,
                torch_dtype=torchType
            )
            self.txt2vid = True
            return pipe

        # WanImageToVideoPipeline - requires special VAE with float32 dtype
        if pipeline_type == "WanImageToVideoPipeline":
            vae = AutoencoderKLWan.from_pretrained(
                request.Model,
                subfolder="vae",
                torch_dtype=torch.float32
            )
            pipe = load_diffusers_pipeline(
                class_name="WanImageToVideoPipeline",
                model_id=request.Model,
                vae=vae,
                torch_dtype=torchType
            )
            self.img2vid = True
            return pipe

        # SanaPipeline - requires special VAE and text encoder dtype conversion
        if pipeline_type == "SanaPipeline":
            pipe = load_diffusers_pipeline(
                class_name="SanaPipeline",
                model_id=request.Model,
                variant="bf16",
                torch_dtype=torch.bfloat16
            )
            pipe.vae.to(torch.bfloat16)
            pipe.text_encoder.to(torch.bfloat16)
            return pipe

        # VideoDiffusionPipeline - alias for DiffusionPipeline with txt2vid flag
        if pipeline_type == "VideoDiffusionPipeline":
            self.txt2vid = True
            pipe = load_diffusers_pipeline(
                class_name="DiffusionPipeline",
                model_id=request.Model,
                torch_dtype=torchType
            )
            return pipe

        # StableVideoDiffusionPipeline - needs img2vid flag and CPU offload
        if pipeline_type == "StableVideoDiffusionPipeline":
            self.img2vid = True
            pipe = load_diffusers_pipeline(
                class_name="StableVideoDiffusionPipeline",
                model_id=request.Model,
                torch_dtype=torchType,
                variant=variant
            )
            if not DISABLE_CPU_OFFLOAD:
                pipe.enable_model_cpu_offload()
            return pipe

        # ================================================================
        # Dynamic pipeline loading - the default path for most pipelines
        # Uses the dynamic loader to instantiate any pipeline by class name
        # ================================================================

        # Build kwargs for dynamic loading
        load_kwargs = {"torch_dtype": torchType}

        # Add variant if not loading from single file
        if not fromSingleFile and variant:
            load_kwargs["variant"] = variant

        # Add use_safetensors for from_pretrained
        if not fromSingleFile:
            load_kwargs["use_safetensors"] = SAFETENSORS

        # Determine pipeline class name - default to AutoPipelineForText2Image
        effective_pipeline_type = pipeline_type if pipeline_type else "AutoPipelineForText2Image"

        # Use dynamic loader for all pipelines
        try:
            pipe = load_diffusers_pipeline(
                class_name=effective_pipeline_type,
                model_id=modelFile if fromSingleFile else request.Model,
                from_single_file=fromSingleFile,
                **load_kwargs
            )
        except Exception as e:
            # Provide helpful error with available pipelines
            available = get_available_pipelines()
            raise ValueError(
                f"Failed to load pipeline '{effective_pipeline_type}': {e}\n"
                f"Available pipelines: {', '.join(available[:30])}..."
            ) from e

        # Apply LowVRAM optimization if supported and requested
        if request.LowVRAM and hasattr(pipe, 'enable_model_cpu_offload'):
            pipe.enable_model_cpu_offload()

        return pipe

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        try:
            print(f"Loading model {request.Model}...", file=sys.stderr)
            print(f"Request {request}", file=sys.stderr)
            torchType = torch.float32
            variant = None

            if request.F16Memory:
                torchType = torch.float16
                variant = "fp16"

            options = request.Options

            # empty dict
            self.options = {}

            # The options are a list of strings in this form optname:optvalue
            # We are storing all the options in a dict so we can use it later when
            # generating the images
            for opt in options:
                if ":" not in opt:
                    continue
                key, value = opt.split(":")
                # if value is a number, convert it to the appropriate type
                if is_float(value):
                    value = float(value)
                elif is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                self.options[key] = value

            # From options, extract if present "torch_dtype" and set it to the appropriate type
            if "torch_dtype" in self.options:
                if self.options["torch_dtype"] == "fp16":
                    torchType = torch.float16
                elif self.options["torch_dtype"] == "bf16":
                    torchType = torch.bfloat16
                elif self.options["torch_dtype"] == "fp32":
                    torchType = torch.float32
                # remove it from options
                del self.options["torch_dtype"]

            print(f"Options: {self.options}", file=sys.stderr)

            local = False
            modelFile = request.Model

            self.cfg_scale = 7
            self.PipelineType = request.PipelineType

            if request.CFGScale != 0:
                self.cfg_scale = request.CFGScale

            clipmodel = "Lykon/dreamshaper-8"
            if request.CLIPModel != "":
                clipmodel = request.CLIPModel
            clipsubfolder = "text_encoder"
            if request.CLIPSubfolder != "":
                clipsubfolder = request.CLIPSubfolder

            # Check if ModelFile exists
            if request.ModelFile != "":
                if os.path.exists(request.ModelFile):
                    local = True
                    modelFile = request.ModelFile

            fromSingleFile = request.Model.startswith("http") or request.Model.startswith("/") or local
            self.img2vid = False
            self.txt2vid = False

            # Load pipeline using dynamic loader
            # Special cases that require custom initialization are handled first
            self.pipe = self._load_pipeline(
                request=request,
                modelFile=modelFile,
                fromSingleFile=fromSingleFile,
                torchType=torchType,
                variant=variant
            )

            if CLIPSKIP and request.CLIPSkip != 0:
                self.clip_skip = request.CLIPSkip
            else:
                self.clip_skip = 0

            # torch_dtype needs to be customized. float16 for GPU, float32 for CPU
            # TODO: this needs to be customized
            if request.SchedulerType != "":
                self.pipe.scheduler = get_scheduler(request.SchedulerType, self.pipe.scheduler.config)

            if COMPEL:
                self.compel = Compel(
                    tokenizer=[self.pipe.tokenizer, self.pipe.tokenizer_2],
                    text_encoder=[self.pipe.text_encoder, self.pipe.text_encoder_2],
                    returned_embeddings_type=ReturnedEmbeddingsType.PENULTIMATE_HIDDEN_STATES_NON_NORMALIZED,
                    requires_pooled=[False, True]
                )

            if request.ControlNet:
                self.controlnet = ControlNetModel.from_pretrained(
                    request.ControlNet, torch_dtype=torchType, variant=variant
                )
                self.pipe.controlnet = self.controlnet
            else:
                self.controlnet = None

            if request.LoraAdapter and not os.path.isabs(request.LoraAdapter):
                # modify LoraAdapter to be relative to modelFileBase
                request.LoraAdapter = os.path.join(request.ModelPath, request.LoraAdapter)

            device = "cpu" if not request.CUDA else "cuda"
            if XPU:
                device = "xpu"
            mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
            if mps_available:
                device = "mps"
            self.device = device
            if request.LoraAdapter:
                # Check if its a local file and not a directory ( we load lora differently for a safetensor file )
                if os.path.exists(request.LoraAdapter) and not os.path.isdir(request.LoraAdapter):
                    self.pipe.load_lora_weights(request.LoraAdapter)
                else:
                    self.pipe.unet.load_attn_procs(request.LoraAdapter)
            if len(request.LoraAdapters) > 0:
                i = 0
                adapters_name = []
                adapters_weights = []
                for adapter in request.LoraAdapters:
                    if not os.path.isabs(adapter):
                        adapter = os.path.join(request.ModelPath, adapter)
                    self.pipe.load_lora_weights(adapter, adapter_name=f"adapter_{i}")
                    adapters_name.append(f"adapter_{i}")
                    i += 1

                for adapters_weight in request.LoraScales:
                    adapters_weights.append(adapters_weight)

                self.pipe.set_adapters(adapters_name, adapter_weights=adapters_weights)

            if device != "cpu":
                self.pipe.to(device)
                if self.controlnet:
                    self.controlnet.to(device)

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    # https://github.com/huggingface/diffusers/issues/3064
    def load_lora_weights(self, checkpoint_path, multiplier, device, dtype):
        LORA_PREFIX_UNET = "lora_unet"
        LORA_PREFIX_TEXT_ENCODER = "lora_te"
        # load LoRA weight from .safetensors
        state_dict = load_file(checkpoint_path, device=device)

        updates = defaultdict(dict)
        for key, value in state_dict.items():
            # it is suggested to print out the key, it usually will be something like below
            # "lora_te_text_model_encoder_layers_0_self_attn_k_proj.lora_down.weight"

            layer, elem = key.split('.', 1)
            updates[layer][elem] = value

        # directly update weight in diffusers model
        for layer, elems in updates.items():

            if "text" in layer:
                layer_infos = layer.split(LORA_PREFIX_TEXT_ENCODER + "_")[-1].split("_")
                curr_layer = self.pipe.text_encoder
            else:
                layer_infos = layer.split(LORA_PREFIX_UNET + "_")[-1].split("_")
                curr_layer = self.pipe.unet

            # find the target layer
            temp_name = layer_infos.pop(0)
            while len(layer_infos) > -1:
                try:
                    curr_layer = curr_layer.__getattr__(temp_name)
                    if len(layer_infos) > 0:
                        temp_name = layer_infos.pop(0)
                    elif len(layer_infos) == 0:
                        break
                except Exception:
                    if len(temp_name) > 0:
                        temp_name += "_" + layer_infos.pop(0)
                    else:
                        temp_name = layer_infos.pop(0)

            # get elements for this layer
            weight_up = elems['lora_up.weight'].to(dtype)
            weight_down = elems['lora_down.weight'].to(dtype)
            alpha = elems['alpha'] if 'alpha' in elems else None
            if alpha:
                alpha = alpha.item() / weight_up.shape[1]
            else:
                alpha = 1.0

            # update weight
            if len(weight_up.shape) == 4:
                curr_layer.weight.data += multiplier * alpha * torch.mm(weight_up.squeeze(3).squeeze(2), weight_down.squeeze(3).squeeze(2)).unsqueeze(2).unsqueeze(3)
            else:
                curr_layer.weight.data += multiplier * alpha * torch.mm(weight_up, weight_down)

    def GenerateImage(self, request, context):

        prompt = request.positive_prompt

        steps = 1

        if request.step != 0:
            steps = request.step

        # create a dictionary of values for the parameters
        options = {
            "num_inference_steps": steps,
        }

        if hasattr(request, 'negative_prompt') and request.negative_prompt != "":
            options["negative_prompt"] = request.negative_prompt

        # Handle image source: prioritize RefImages over request.src
        image_src = None
        if hasattr(request, 'ref_images') and request.ref_images and len(request.ref_images) > 0:
            # Use the first reference image if available
            image_src = request.ref_images[0]
            print(f"Using reference image: {image_src}", file=sys.stderr)
        elif request.src != "":
            # Fall back to request.src if no ref_images
            image_src = request.src
            print(f"Using source image: {image_src}", file=sys.stderr)
        else:
            print("No image source provided", file=sys.stderr)
        
        if image_src and not self.controlnet and not self.img2vid:
            image = Image.open(image_src)
            options["image"] = image
        elif self.controlnet and image_src:
            pose_image = load_image(image_src)
            options["image"] = pose_image

        if CLIPSKIP and self.clip_skip != 0:
            options["clip_skip"] = self.clip_skip

        kwargs = {}

        # populate kwargs from self.options.
        kwargs.update(self.options)

        # Set seed
        if request.seed > 0:
            kwargs["generator"] = torch.Generator(device=self.device).manual_seed(
                request.seed
            )

        if self.PipelineType == "FluxPipeline":
            kwargs["max_sequence_length"] = 256

        if request.width:
            kwargs["width"] = request.width

        if request.height:
            kwargs["height"] = request.height

        if self.PipelineType == "FluxTransformer2DModel":
            kwargs["output_type"] = "pil"
            kwargs["generator"] = torch.Generator("cpu").manual_seed(0)

        if self.img2vid:
            # Load the conditioning image
            if image_src:
                image = load_image(image_src)
            else:
                # Fallback to request.src for img2vid if no ref_images
                image = load_image(request.src)
            image = image.resize((1024, 576))

            generator = torch.manual_seed(request.seed)
            frames = self.pipe(image, guidance_scale=self.cfg_scale, decode_chunk_size=CHUNK_SIZE, generator=generator).frames[0]
            export_to_video(frames, request.dst, fps=FPS)
            return backend_pb2.Result(message="Media generated successfully", success=True)

        if self.txt2vid:
            video_frames = self.pipe(prompt, guidance_scale=self.cfg_scale, num_inference_steps=steps, num_frames=int(FRAMES)).frames
            export_to_video(video_frames, request.dst)
            return backend_pb2.Result(message="Media generated successfully", success=True)

        print(f"Generating image with {kwargs=}", file=sys.stderr)
        image = {}
        if COMPEL:
            conditioning, pooled = self.compel.build_conditioning_tensor(prompt)
            kwargs["prompt_embeds"] = conditioning
            kwargs["pooled_prompt_embeds"] = pooled
            # pass the kwargs dictionary to the self.pipe method
            image = self.pipe(
                guidance_scale=self.cfg_scale,
                **kwargs
            ).images[0]
        else:
            # pass the kwargs dictionary to the self.pipe method
            image = self.pipe(
                prompt,
                guidance_scale=self.cfg_scale,
                **kwargs
            ).images[0]

        # save the result
        image.save(request.dst)

        return backend_pb2.Result(message="Media generated", success=True)

    def GenerateVideo(self, request, context):
        try:
            prompt = request.prompt
            if not prompt:
                return backend_pb2.Result(success=False, message="No prompt provided for video generation")

            # Set default values from request or use defaults
            num_frames = request.num_frames if request.num_frames > 0 else 81
            fps = request.fps if request.fps > 0 else 16
            cfg_scale = request.cfg_scale if request.cfg_scale > 0 else 4.0
            num_inference_steps = request.step if request.step > 0 else 40
            
            # Prepare generation parameters
            kwargs = {
                "prompt": prompt,
                "negative_prompt": request.negative_prompt if request.negative_prompt else "",
                "height": request.height if request.height > 0 else 720,
                "width": request.width if request.width > 0 else 1280,
                "num_frames": num_frames,
                "guidance_scale": cfg_scale,
                "num_inference_steps": num_inference_steps,
            }

            # Add custom options from self.options (including guidance_scale_2 if specified)
            kwargs.update(self.options)

            # Set seed if provided
            if request.seed > 0:
                kwargs["generator"] = torch.Generator(device=self.device).manual_seed(request.seed)

            # Handle start and end images for video generation
            if request.start_image:
                kwargs["start_image"] = load_image(request.start_image)
            if request.end_image:
                kwargs["end_image"] = load_image(request.end_image)

            print(f"Generating video with {kwargs=}", file=sys.stderr)

            # Generate video frames based on pipeline type
            if self.PipelineType == "WanPipeline":
                # WAN2.2 text-to-video generation
                output = self.pipe(**kwargs)
                frames = output.frames[0]  # WAN2.2 returns frames in this format
            elif self.PipelineType == "WanImageToVideoPipeline":
                # WAN2.2 image-to-video generation
                if request.start_image:
                    # Load and resize the input image according to WAN2.2 requirements
                    image = load_image(request.start_image)
                    # Use request dimensions or defaults, but respect WAN2.2 constraints
                    request_height = request.height if request.height > 0 else 480
                    request_width = request.width if request.width > 0 else 832
                    max_area = request_height * request_width
                    aspect_ratio = image.height / image.width
                    mod_value = self.pipe.vae_scale_factor_spatial * self.pipe.transformer.config.patch_size[1]
                    height = round((max_area * aspect_ratio) ** 0.5 / mod_value) * mod_value
                    width = round((max_area / aspect_ratio) ** 0.5 / mod_value) * mod_value
                    image = image.resize((width, height))
                    kwargs["image"] = image
                    kwargs["height"] = height
                    kwargs["width"] = width
                
                output = self.pipe(**kwargs)
                frames = output.frames[0]
            elif self.img2vid:
                # Generic image-to-video generation
                if request.start_image:
                    image = load_image(request.start_image)
                    image = image.resize((request.width if request.width > 0 else 1024, 
                                       request.height if request.height > 0 else 576))
                    kwargs["image"] = image
                
                output = self.pipe(**kwargs)
                frames = output.frames[0]
            elif self.txt2vid:
                # Generic text-to-video generation
                output = self.pipe(**kwargs)
                frames = output.frames[0]
            else:
                return backend_pb2.Result(success=False, message=f"Pipeline {self.PipelineType} does not support video generation")

            # Export video
            export_to_video(frames, request.dst, fps=fps)
            
            return backend_pb2.Result(message="Video generated successfully", success=True)

        except Exception as err:
            print(f"Error generating video: {err}", file=sys.stderr)
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"Error generating video: {err}")


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
        server.stop(0)
        sys.exit(0)

    # Set the signal handlers for SIGINT and SIGTERM
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
