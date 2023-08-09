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

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

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

            if request.PipelineType == "":
                request.PipelineType == "StableDiffusionPipeline"

            if request.PipelineType == "StableDiffusionPipeline":
                self.pipe = StableDiffusionPipeline.from_pretrained(request.Model,
                                                               torch_dtype=torchType)

            if request.PipelineType == "DiffusionPipeline":
                self.pipe = DiffusionPipeline.from_pretrained(request.Model,
                                                               torch_dtype=torchType)

            if request.PipelineType == "StableDiffusionXLPipeline":
                self.pipe = StableDiffusionXLPipeline.from_pretrained(
                    request.Model, 
                    torch_dtype=torchType, 
                    use_safetensors=True, 
             #       variant="fp16"
                    )
          
            # torch_dtype needs to be customized. float16 for GPU, float32 for CPU
            # TODO: this needs to be customized
            if request.SchedulerType == "EulerAncestralDiscreteScheduler":
                self.pipe.scheduler = EulerAncestralDiscreteScheduler.from_config(self.pipe.scheduler.config)
            if request.SchedulerType == "DPMSolverMultistepScheduler":
                self.pipe.scheduler = DPMSolverMultistepScheduler.from_config(self.pipe.scheduler.config)

            if request.CUDA:
                self.pipe.to('cuda')
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)
    def GenerateImage(self, request, context):

        prompt = request.positive_prompt
        negative_prompt = request.negative_prompt

        image = self.pipe(
            prompt, 
            negative_prompt=negative_prompt, 
            width=request.width,
            height=request.height,
      #      guidance_scale=12,
            target_size=(request.width,request.height),
            original_size=(4096,4096),
            num_inference_steps=request.step
            ).images[0]

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