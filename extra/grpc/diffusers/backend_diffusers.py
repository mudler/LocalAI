#!/usr/bin/env python3
import grpc
from concurrent import futures
import time
import backend_pb2
import backend_pb2_grpc
import argparse
import signal
import sys
import os

# import diffusers
import torch
from torch import autocast
from diffusers import StableDiffusionXLPipeline, DPMSolverMultistepScheduler, StableDiffusionPipeline, DiffusionPipeline, EulerAncestralDiscreteScheduler
from diffusers.pipelines.stable_diffusion import safety_checker
from compel import Compel

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
COMPEL=os.environ.get("COMPEL", "1") == "1"

# https://github.com/CompVis/stable-diffusion/issues/239#issuecomment-1627615287
def sc(self, clip_input, images) : return images, [False for i in images]
# edit the StableDiffusionSafetyChecker class so that, when called, it just returns the images and an array of True values
safety_checker.StableDiffusionSafetyChecker.forward = sc

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):
        try:
            print(f"Loading model {request.Model}...", file=sys.stderr)
            print(f"Request {request}", file=sys.stderr)
            torchType = torch.float32
            if request.F16Memory:
                torchType = torch.float16

            local = False
            modelFile = request.Model

            cfg_scale = 7
            if request.CFGScale != 0:
                cfg_scale = request.CFGScale

            # Check if ModelFile exists
            if request.ModelFile != "":
                if os.path.exists(request.ModelFile):
                    local = True
                    modelFile = request.ModelFile

            fromSingleFile = request.Model.startswith("http") or request.Model.startswith("/") or local
            # If request.Model is a URL, use from_single_file
                

            if request.PipelineType == "":
                request.PipelineType == "StableDiffusionPipeline"

            if request.PipelineType == "StableDiffusionPipeline":
                if fromSingleFile:
                    self.pipe = StableDiffusionPipeline.from_single_file(modelFile,
                                                               torch_dtype=torchType,
                                                               guidance_scale=cfg_scale)
                else:
                    self.pipe = StableDiffusionPipeline.from_pretrained(request.Model,
                                                               torch_dtype=torchType,
                                                               guidance_scale=cfg_scale)

            if request.PipelineType == "DiffusionPipeline":
                if fromSingleFile:
                    self.pipe = DiffusionPipeline.from_single_file(modelFile,
                                                               torch_dtype=torchType,
                                                               guidance_scale=cfg_scale)
                else:
                    self.pipe = DiffusionPipeline.from_pretrained(request.Model,
                                                               torch_dtype=torchType,
                                                               guidance_scale=cfg_scale)

            if request.PipelineType == "StableDiffusionXLPipeline":
                if fromSingleFile:
                    self.pipe = StableDiffusionXLPipeline.from_single_file(modelFile,
                                                               torch_dtype=torchType, use_safetensors=True,
                                                               guidance_scale=cfg_scale)
                else:
                    self.pipe = StableDiffusionXLPipeline.from_pretrained(
                        request.Model, 
                        torch_dtype=torchType, 
                        use_safetensors=True, 
                #       variant="fp16"
                        guidance_scale=cfg_scale)
          
            # torch_dtype needs to be customized. float16 for GPU, float32 for CPU
            # TODO: this needs to be customized
            if request.SchedulerType == "EulerAncestralDiscreteScheduler":
                self.pipe.scheduler = EulerAncestralDiscreteScheduler.from_config(self.pipe.scheduler.config)
            if request.SchedulerType == "DPMSolverMultistepScheduler":
                self.pipe.scheduler = DPMSolverMultistepScheduler.from_config(self.pipe.scheduler.config)
            if request.SchedulerType == "DPMSolverMultistepScheduler++":
                self.pipe.scheduler = DPMSolverMultistepScheduler.from_config(self.pipe.scheduler.config,algorithm_type="dpmsolver++")
            if request.SchedulerType == "DPMSolverMultistepSchedulerSDE++":
                self.pipe.scheduler = DPMSolverMultistepScheduler.from_config(self.pipe.scheduler.config, algorithm_type="sde-dpmsolver++")
            if request.CUDA:
                self.pipe.to('cuda')

            self.compel = Compel(tokenizer=self.pipe.tokenizer, text_encoder=self.pipe.text_encoder)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)
    def GenerateImage(self, request, context):

        prompt = request.positive_prompt

        # create a dictionary of values for the parameters
        options = {
            "negative_prompt":     request.negative_prompt, 
            "width":               request.width, 
            "height":              request.height,
            "num_inference_steps": request.step
        }

        # Get the keys that we will build the args for our pipe for
        keys = options.keys()

        if request.EnableParameters != "":
            keys = request.EnableParameters.split(",")

        if request.EnableParameters == "none":
            keys = []

        # create a dictionary of parameters by using the keys from EnableParameters and the values from defaults
        kwargs = {key: options[key] for key in keys}
        image = {}
        if COMPEL:
            conditioning = self.compel.build_conditioning_tensor(prompt)
            kwargs["prompt_embeds"]= conditioning
            # pass the kwargs dictionary to the self.pipe method
            image = self.pipe( 
                **kwargs
                ).images[0] 
        else:
            # pass the kwargs dictionary to the self.pipe method
            image = self.pipe(
                prompt, 
                **kwargs
                ).images[0]

        # save the result
        image.save(request.dst)

        return backend_pb2.Result(message="Model loaded successfully", success=True)

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
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