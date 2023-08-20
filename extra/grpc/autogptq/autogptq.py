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
from auto_gptq import AutoGPTQForCausalLM, BaseQuantizeConfig
from pathlib import Path
from transformers import AutoTokenizer
from transformers import TextGenerationPipeline

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):
        try:
            device = "cuda:0"
            if request.Device != "":
                device = request.Device

            tokenizer = AutoTokenizer.from_pretrained(request.Model, use_fast=request.UseFastTokenizer)

            model = AutoGPTQForCausalLM.from_quantized(request.Model,
                    model_basename=request.ModelBaseName,
                    use_safetensors=True,
                    trust_remote_code=True,
                    device=device,
                    use_triton=request.UseTriton,
                    quantize_config=None)
            
            self.model = model
            self.tokenizer = tokenizer
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Predict(self, request, context):
        penalty = 1.0
        if request.Penalty != 0.0:
            penalty = request.Penalty
        tokens = 512
        if request.Tokens != 0:
            tokens = request.Tokens
        top_p = 0.95
        if request.TopP != 0.0:
            top_p = request.TopP

        # Implement Predict RPC
        pipeline = TextGenerationPipeline(
            model=self.model, 
            tokenizer=self.tokenizer,
            max_new_tokens=tokens,
            temperature=request.Temperature,
            top_p=top_p,
            repetition_penalty=penalty,
            )
        t = pipeline(request.Prompt)[0]["generated_text"]
        # Remove prompt from response if present
        if request.Prompt in t:
            t = t.replace(request.Prompt, "")

        return backend_pb2.Result(message=bytes(t, encoding='utf-8'))

    def PredictStream(self, request, context):
        # Implement PredictStream RPC
        #for reply in some_data_generator():
        #    yield reply
        # Not implemented yet
        return self.Predict(request, context)


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=1))
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