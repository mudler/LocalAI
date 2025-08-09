#!/usr/bin/env python3
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

from diffusers import SanaPipeline, StableDiffusion3Pipeline, StableDiffusionXLPipeline, StableDiffusionDepth2ImgPipeline, DPMSolverMultistepScheduler, StableDiffusionPipeline, DiffusionPipeline, \
    EulerAncestralDiscreteScheduler, FluxPipeline, FluxTransformer2DModel
from diffusers import StableDiffusionImg2ImgPipeline, AutoPipelineForText2Image, ControlNetModel, StableVideoDiffusionPipeline, Lumina2Text2ImgPipeline
from diffusers.pipelines.stable_diffusion import safety_checker
from diffusers.utils import load_image, export_to_video
from compel import Compel, ReturnedEmbeddingsType
from optimum.quanto import freeze, qfloat8, quantize
from transformers import CLIPTextModel, T5EncoderModel
from safetensors.torch import load_file

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
COMPEL = os.environ.get("COMPEL", "0") == "1"
# Attempt to use XPU only if Torch says it is available when asking for it
XPU = ((os.environ.get("XPU", "0") == "1") & (torch.xpu.is_available()))
CLIPSKIP = os.environ.get("CLIPSKIP", "1") == "1"
SAFETENSORS = os.environ.get("SAFETENSORS", "1") == "1"
CHUNK_SIZE = os.environ.get("CHUNK_SIZE", "8")
FPS = os.environ.get("FPS", "7")
DISABLE_CPU_OFFLOAD = os.environ.get("DISABLE_CPU_OFFLOAD", "0") == "1"
FRAMES = os.environ.get("FRAMES", "64")

# Set Torch to use all logical CPU cores for CPU mode
num_cores = os.cpu_count()
torch.set_num_threads(max(1, num_cores // 2))
torch.set_num_interop_threads(num_cores)

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
    try:
        float(s)
        return True
    except ValueError:
        return False

def is_int(s):
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
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        try:
            print(f"Loading model {request.Model}...", file=sys.stderr)
            print(f"Request {request}", file=sys.stderr)
            torchType = torch.float32
            variant = None

            # Only use f16 if not running on CPU - forcing f16 on CPU causes freezes (https://github.com/pytorch/pytorch/issues/75458)
            if (request.F16Memory & ((request.CUDA & torch.cuda.is_available()) | XPU)):
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
                self.options[key] = value

            # From options, extract if present "torch_dtype" and set it to the appropriate type; if on CPU, always force float32
            if "torch_dtype" in self.options:
                if self.options["torch_dtype"] == "fp16":
                    if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        torchType = torch.float32
                    else:
                        torchType = torch.float16
                elif self.options["torch_dtype"] == "bf16":
                    if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        torchType = torch.float32
                    else:
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
            ## img2img
            if (request.PipelineType == "StableDiffusionImg2ImgPipeline") or (request.IMG2IMG and request.PipelineType == ""):
                if fromSingleFile:
                    self.pipe = StableDiffusionImg2ImgPipeline.from_single_file(modelFile,
                                                                                torch_dtype=torchType)
                else:
                    self.pipe = StableDiffusionImg2ImgPipeline.from_pretrained(request.Model,
                                                                               torch_dtype=torchType)

            elif request.PipelineType == "StableDiffusionDepth2ImgPipeline":
                self.pipe = StableDiffusionDepth2ImgPipeline.from_pretrained(request.Model,
                                                                             torch_dtype=torchType)
            ## img2vid
            elif request.PipelineType == "StableVideoDiffusionPipeline":
                self.img2vid = True
                self.pipe = StableVideoDiffusionPipeline.from_pretrained(
                    request.Model, torch_dtype=torchType, variant=variant
                )
                if not DISABLE_CPU_OFFLOAD:
                    self.pipe.enable_model_cpu_offload()
            ## text2img
            elif request.PipelineType == "AutoPipelineForText2Image" or request.PipelineType == "":
                self.pipe = AutoPipelineForText2Image.from_pretrained(request.Model,
                                                                      torch_dtype=torchType,
                                                                      use_safetensors=SAFETENSORS,
                                                                      variant=variant)
            elif request.PipelineType == "StableDiffusionPipeline":
                if fromSingleFile:
                    self.pipe = StableDiffusionPipeline.from_single_file(modelFile,
                                                                         torch_dtype=torchType)
                else:
                    self.pipe = StableDiffusionPipeline.from_pretrained(request.Model,
                                                                        torch_dtype=torchType)
            elif request.PipelineType == "DiffusionPipeline":
                self.pipe = DiffusionPipeline.from_pretrained(request.Model,
                                                              torch_dtype=torchType)
            elif request.PipelineType == "VideoDiffusionPipeline":
                self.txt2vid = True
                self.pipe = DiffusionPipeline.from_pretrained(request.Model,
                                                              torch_dtype=torchType)
            elif request.PipelineType == "StableDiffusionXLPipeline":
                if fromSingleFile:
                    self.pipe = StableDiffusionXLPipeline.from_single_file(modelFile,
                                                                           torch_dtype=torchType,
                                                                           use_safetensors=True)
                else:
                    self.pipe = StableDiffusionXLPipeline.from_pretrained(
                        request.Model,
                        torch_dtype=torchType,
                        use_safetensors=True,
                        variant=variant)
            elif request.PipelineType == "StableDiffusion3Pipeline":
                if fromSingleFile:
                    self.pipe = StableDiffusion3Pipeline.from_single_file(modelFile,
                                                                          torch_dtype=torchType,
                                                                          use_safetensors=True)
                else:
                    self.pipe = StableDiffusion3Pipeline.from_pretrained(
                        request.Model,
                        torch_dtype=torchType,
                        use_safetensors=True,
                        variant=variant)
            elif request.PipelineType == "FluxPipeline":
                if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        raise RuntimeError("Flux requires f16. Cannot run diffusers using f16 on CPU - doing so causes deadlocks. Refer to: https://github.com/pytorch/pytorch/issues/75458")
                if fromSingleFile:
                    self.pipe = FluxPipeline.from_single_file(modelFile,
                                                              torch_dtype=torchType,
                                                              use_safetensors=True)
                else:
                    self.pipe = FluxPipeline.from_pretrained(
                        request.Model,
                        torch_dtype=torch.bfloat16)
                if request.LowVRAM:
                    self.pipe.enable_model_cpu_offload()
            elif request.PipelineType == "FluxTransformer2DModel":
                    if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        raise RuntimeError("Flux requires f16. Cannot run diffusers using f16 on CPU - doing so causes deadlocks. Refer to: https://github.com/pytorch/pytorch/issues/75458")
                    dtype = torch.bfloat16
                    # specify from environment or default to "ChuckMcSneed/FLUX.1-dev"
                    bfl_repo = os.environ.get("BFL_REPO", "ChuckMcSneed/FLUX.1-dev")

                    transformer = FluxTransformer2DModel.from_single_file(modelFile, torch_dtype=dtype)
                    quantize(transformer, weights=qfloat8)
                    freeze(transformer)
                    text_encoder_2 = T5EncoderModel.from_pretrained(bfl_repo, subfolder="text_encoder_2", torch_dtype=dtype)
                    quantize(text_encoder_2, weights=qfloat8)
                    freeze(text_encoder_2)

                    self.pipe = FluxPipeline.from_pretrained(bfl_repo, transformer=None, text_encoder_2=None, torch_dtype=dtype)
                    self.pipe.transformer = transformer
                    self.pipe.text_encoder_2 = text_encoder_2

                    if request.LowVRAM:
                        self.pipe.enable_model_cpu_offload()
            elif request.PipelineType == "Lumina2Text2ImgPipeline":
                if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        raise RuntimeError("Lumina requires f16. Cannot run diffusers using f16 on CPU - doing so causes deadlocks. Refer to: https://github.com/pytorch/pytorch/issues/75458")
                self.pipe = Lumina2Text2ImgPipeline.from_pretrained(
                    request.Model,
                    torch_dtype=torch.bfloat16)
                if request.LowVRAM:
                    self.pipe.enable_model_cpu_offload()
            elif request.PipelineType == "SanaPipeline":
                if not ((request.CUDA & torch.cuda.is_available()) | XPU):
                        raise RuntimeError("Sana requires f16. Cannot run diffusers using f16 on CPU - doing so causes deadlocks. Refer to: https://github.com/pytorch/pytorch/issues/75458")
                self.pipe = SanaPipeline.from_pretrained(
                    request.Model,
                    variant="bf16",
                    torch_dtype=torch.bfloat16)
                self.pipe.vae.to(torch.bfloat16)
                self.pipe.text_encoder.to(torch.bfloat16)

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

            device = "cpu" if not (request.CUDA & torch.cuda.is_available()) else "cuda"
            if XPU:
                device = "xpu"
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
            else:
                self.pipe.to("cpu")

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
            "negative_prompt": request.negative_prompt,
            "num_inference_steps": steps,
        }

        if request.src != "" and not self.controlnet and not self.img2vid:
            image = Image.open(request.src)
            options["image"] = image
        elif self.controlnet and request.src:
            pose_image = load_image(request.src)
            options["image"] = pose_image

        if CLIPSKIP and self.clip_skip != 0:
            options["clip_skip"] = self.clip_skip

        # Get the keys that we will build the args for our pipe for
        keys = options.keys()

        if request.EnableParameters != "":
            keys = [key.strip() for key in request.EnableParameters.split(",")]

        if request.EnableParameters == "none":
            keys = []

        # create a dictionary of parameters by using the keys from EnableParameters and the values from defaults
        kwargs = {key: options.get(key) for key in keys if key in options}

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
