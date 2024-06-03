#!/usr/bin/env python3
"""
Extra gRPC server for Rerankers models.
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

from rerankers import Reranker

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

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
            kwargs = {}
            if request.Type != "":
                kwargs['model_type'] = request.Type
            if request.PipelineType != "": # Reuse the PipelineType field for language
                kwargs['lang'] = request.PipelineType
            self.model_name = model_name
            self.model = Reranker(model_name, **kwargs)  
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Rerank(self, request, context):
        documents = []
        for idx, doc in enumerate(request.documents):
            documents.append(doc)
        ranked_results=self.model.rank(query=request.query, docs=documents, doc_ids=list(range(len(request.documents))))
        # Prepare results to return
        results = [
            backend_pb2.DocumentResult(
                index=res.doc_id,
                text=res.text,
                relevance_score=res.score
            ) for res in ranked_results.results
        ]

        # Calculate the usage and total tokens
        # TODO: Implement the usage calculation with reranker
        total_tokens = sum(len(doc.split()) for doc in request.documents) + len(request.query.split())
        prompt_tokens = len(request.query.split())
        usage = backend_pb2.Usage(total_tokens=total_tokens, prompt_tokens=prompt_tokens)
        return backend_pb2.RerankResult(usage=usage, results=results)

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
