#!/usr/bin/env python3
from concurrent import futures
import argparse
import signal
import sys
import os
import time

import grpc
import backend_pb2
import backend_pb2_grpc
from auto_gptq.modeling._base import BaseGPTQForCausalLM
from auto_gptq import AutoGPTQForCausalLM, BaseQuantizeConfig
from transformers import AutoTokenizer, AutoModelForCausalLM
from transformers import TextGenerationPipeline

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

class InternLMXComposer2QForCausalLM(BaseGPTQForCausalLM):
    layers_block_name = "model.layers"
    outside_layer_modules = [
        'vit', 'vision_proj', 'model.tok_embeddings', 'model.norm', 'output',
    ]
    inside_layer_modules = [
        ["attention.wqkv.linear"],
        ["attention.wo.linear"],
        ["feed_forward.w1.linear", "feed_forward.w3.linear"],
        ["feed_forward.w2.linear"],
    ]


# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):
        try:
            device = "cuda:0"
            if request.Device != "":
                device = request.Device

            # support loading local model files
            model_path = os.path.join(os.environ.get('MODELS_PATH', './'), request.Model)
            tokenizer = AutoTokenizer.from_pretrained(model_path, use_fast=True, trust_remote_code=request.TrustRemoteCode)

            # support model `internlm/internlm-xcomposer2-vl-7b-4bit`
            if "xcomposer2-vl" in request.Model.lower():
                model = InternLMXComposer2QForCausalLM.from_quantized(model_path, 
                    trust_remote_code=request.TrustRemoteCode, 
                    # maybe add this to request params?
                    use_marlin=True,
                    use_triton=request.UseTriton,
                    device=device).eval()
            # support model `Qwen/Qwen-VL-Chat-Int4`
            elif "qwen-vl" in request.Model.lower():
                model = AutoModelForCausalLM.from_pretrained(model_path, 
                    trust_remote_code=request.TrustRemoteCode,
                    use_triton=request.UseTriton,
                    device_map="auto").eval()
            else:
                model = AutoGPTQForCausalLM.from_quantized(model_path,
                    model_basename=request.ModelBaseName,
                    use_safetensors=True,
                    trust_remote_code=request.TrustRemoteCode,
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