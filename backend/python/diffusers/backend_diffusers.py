#!/usr/bin/env python3
from concurrent import futures

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

from diffusers import StableDiffusionXLPipeline, StableDiffusionDepth2ImgPipeline, DPMSolverMultistepScheduler, StableDiffusionPipeline, DiffusionPipeline, EulerAncestralDiscreteScheduler
from diffusers import StableDiffusionImg2ImgPipeline, AutoPipelineForText2Image, ControlNetModel, StableVideoDiffusionPipeline
from diffusers.pipelines.stable_diffusion import safety_checker
from diffusers.utils import load_image,export_to_video
from compel import Compel

from transformers import CLIPTextModel
from safetensors.torch import load_file


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
COMPEL=os.environ.get("COMPEL", "1") == "1"
CLIPSKIP=os.environ.get("CLIPSKIP", "1") == "1"
SAFETENSORS=os.environ.get("SAFETENSORS", "1") == "1"
CHUNK_SIZE=os.environ.get("CHUNK_SIZE", "8")
FPS=os.environ.get("FPS", "7")
DISABLE_CPU_OFFLOAD=os.environ.get("DISABLE_CPU_OFFLOAD", "0") == "1"
FRAMES=os.environ.get("FRAMES", "64")

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# https://github.com/CompVis/stable-diffusion/issues/239#issuecomment-1627615287
def sc(self, clip_input, images) : return images, [False for i in images]
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

            if request.F16Memory:
                torchType = torch.float16
                variant="fp16"

            local = False
            modelFile = request.Model

            self.cfg_scale = 7
            if request.CFGScale != 0:
                self.cfg_scale = request.CFGScale
            
            clipmodel = "runwayml/stable-diffusion-v1-5"
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
            self.img2vid=False
            self.txt2vid=False
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
                self.img2vid=True
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
                self.txt2vid=True
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

            if CLIPSKIP and request.CLIPSkip != 0:
                self.clip_skip = request.CLIPSkip
            else:
                self.clip_skip = 0
            
            # torch_dtype needs to be customized. float16 for GPU, float32 for CPU
            # TODO: this needs to be customized
            if request.SchedulerType != "":
                self.pipe.scheduler = get_scheduler(request.SchedulerType, self.pipe.scheduler.config)
                
            if not self.img2vid:
                self.compel = Compel(tokenizer=self.pipe.tokenizer, text_encoder=self.pipe.text_encoder)


            if request.ControlNet:
                self.controlnet = ControlNetModel.from_pretrained(
                    request.ControlNet, torch_dtype=torchType, variant=variant
                )
                self.pipe.controlnet = self.controlnet
            else:
                self.controlnet = None

            if request.CUDA:
                self.pipe.to('cuda')
                if self.controlnet:
                    self.controlnet.to('cuda')
            # Assume directory from request.ModelFile.
            # Only if request.LoraAdapter it's not an absolute path
            if request.LoraAdapter and request.ModelFile != "" and not os.path.isabs(request.LoraAdapter) and request.LoraAdapter:
                # get base path of modelFile
                modelFileBase = os.path.dirname(request.ModelFile)
                # modify LoraAdapter to be relative to modelFileBase
                request.LoraAdapter = os.path.join(modelFileBase, request.LoraAdapter)
            device = "cpu" if not request.CUDA else "cuda"
            self.device = device
            if request.LoraAdapter:
                # Check if its a local file and not a directory ( we load lora differently for a safetensor file )
                if os.path.exists(request.LoraAdapter) and not os.path.isdir(request.LoraAdapter):
                    self.load_lora_weights(request.LoraAdapter, 1, device, torchType)
                else:
                    self.pipe.unet.load_attn_procs(request.LoraAdapter)

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
            "negative_prompt":     request.negative_prompt, 
            "width":               request.width, 
            "height":              request.height,
            "num_inference_steps": steps,
        }

        if request.src != "" and not self.controlnet and not self.img2vid:
            image = Image.open(request.src)
            options["image"] = image
        elif self.controlnet and request.src:
            pose_image = load_image(request.src)
            options["image"] = pose_image

        if CLIPSKIP and self.clip_skip != 0:
            options["clip_skip"]=self.clip_skip

        # Get the keys that we will build the args for our pipe for
        keys = options.keys()

        if request.EnableParameters != "":
            keys = request.EnableParameters.split(",")

        if request.EnableParameters == "none":
            keys = []

        # create a dictionary of parameters by using the keys from EnableParameters and the values from defaults
        kwargs = {key: options[key] for key in keys}

        # Set seed
        if request.seed > 0:
            kwargs["generator"] = torch.Generator(device=self.device).manual_seed(
                request.seed
            )

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

        image = {}
        if COMPEL:
            conditioning = self.compel.build_conditioning_tensor(prompt)
            kwargs["prompt_embeds"]= conditioning
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
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
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