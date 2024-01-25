#!/usr/bin/env python3
"""
Extra gRPC server for HuggingFace AutoModel models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os

import time
import backend_pb2
import backend_pb2_grpc

import grpc
import torch
import torch.cuda
from transformers import AutoTokenizer, AutoModel, AutoModelForCausalLM, set_seed

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def mean_pooling(model_output, attention_mask):
    """
    Mean pooling to get sentence embeddings. See:
    https://huggingface.co/sentence-transformers/paraphrase-distilroberta-base-v1
    """
    token_embeddings = model_output[0]
    input_mask_expanded = attention_mask.unsqueeze(-1).expand(token_embeddings.size()).float()
    sum_embeddings = torch.sum(token_embeddings * input_mask_expanded, 1) # Sum columns
    sum_mask = torch.clamp(input_mask_expanded.sum(1), min=1e-9)
    return sum_embeddings / sum_mask

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer for the backend service.

    This class implements the gRPC methods for the backend service, including Health, LoadModel, and Embedding.
    """
    def Health(self, request, context):
        """
        A gRPC method that returns the health status of the backend service.

        Args:
            request: A HealthRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Reply object that contains the health status of the backend service.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        A gRPC method that loads a model into memory.

        Args:
            request: A LoadModelRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Result object that contains the result of the LoadModel operation.
        """
        model_name = request.Model
        try:
            if request.Type == "AutoModelForCausalLM":
                self.model = AutoModelForCausalLM.from_pretrained(model_name, trust_remote_code=True)
            else:
                self.model = AutoModel.from_pretrained(model_name, trust_remote_code=True)

            self.tokenizer = AutoTokenizer.from_pretrained(model_name)
            self.CUDA = False

            if request.CUDA or torch.cuda.is_available():
                try:
                    print("Loading model", model_name, "to CUDA.", file=sys.stderr)
                    self.model = self.model.to("cuda")
                    self.CUDA = True
                except Exception as err:
                    print("Not using CUDA:", err, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """

        set_seed(request.Seed)
        # Tokenize input
        max_length = 512
        if request.Tokens != 0:
            max_length = request.Tokens
        encoded_input = self.tokenizer(request.Embeddings, padding=True, truncation=True, max_length=max_length, return_tensors="pt")    

        # Create word embeddings
        model_output = self.model(**encoded_input)

        # Pool to get sentence embeddings; i.e. generate one 1024 vector for the entire sentence
        sentence_embeddings = mean_pooling(model_output, encoded_input['attention_mask']).detach().numpy()
        print("Calculated embeddings for: " + request.Embeddings, file=sys.stderr)
        print("Embeddings:", sentence_embeddings, file=sys.stderr)
        return backend_pb2.EmbeddingResult(embeddings=sentence_embeddings)

    def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        set_seed(request.Seed)
        if request.TopP == 0:
            request.TopP = 0.9

        max_tokens = 200
        if request.Tokens > 0:
            max_tokens = request.Tokens

        inputs = self.tokenizer(request.Prompt, return_tensors="pt").input_ids
        if self.CUDA:
            inputs = inputs.to("cuda")

        outputs = self.model.generate(inputs,max_new_tokens=max_tokens, temperature=request.Temperature, top_p=request.TopP)

        generated_text = self.tokenizer.batch_decode(outputs, skip_special_tokens=True)[0]
        # Remove prompt from response if present
        if request.Prompt in generated_text:
            generated_text = generated_text.replace(request.Prompt, "")

        return backend_pb2.Reply(message=bytes(generated_text, encoding='utf-8'))

    def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The predict stream result.
        """
        yield self.Predict(request, context)


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
