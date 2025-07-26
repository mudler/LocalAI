#!/usr/bin/env python3
"""
gRPC server for RFDETR object detection models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
import time
import base64
import backend_pb2
import backend_pb2_grpc
import grpc

import requests

import supervision as sv
from inference import get_model
from PIL import Image
from io import BytesIO

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer for the RFDETR backend service.

    This class implements the gRPC methods for object detection using RFDETR models.
    """
    
    def __init__(self):
        self.model = None
        self.model_name = None
        
    def Health(self, request, context):
        """
        A gRPC method that returns the health status of the backend service.

        Args:
            request: A HealthMessage object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Reply object that contains the health status of the backend service.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        A gRPC method that loads a RFDETR model into memory.

        Args:
            request: A ModelOptions object that contains the model parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Result object that contains the result of the LoadModel operation.
        """
        model_name = request.Model
        try:
            # Load the RFDETR model
            self.model = get_model(model_name)
            self.model_name = model_name
            print(f'Loaded RFDETR model: {model_name}')
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Failed to load model: {err}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Detect(self, request, context):
        """
        A gRPC method that performs object detection on an image.

        Args:
            request: A DetectOptions object that contains the image source.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A DetectResponse object that contains the detection results.
        """
        if self.model is None:
            print(f"Model is None")
            return backend_pb2.DetectResponse()
        print(f"Model is not None")
        try:
            print(f"Decoding image")
            # Decode the base64 image
            print(f"Image data: {request.src}")

            image_data = base64.b64decode(request.src)
            image = Image.open(BytesIO(image_data))
            
            # Perform inference
            predictions = self.model.infer(image, confidence=0.5)[0]
          
            # Convert to proto format
            proto_detections = []
            for i in range(len(predictions.predictions)):
                pred = predictions.predictions[i]
                print(f"Prediction: {pred}")
                proto_detection = backend_pb2.Detection(
                    x=float(pred.x),
                    y=float(pred.y),
                    width=float(pred.width),
                    height=float(pred.height),
                    confidence=float(pred.confidence),
                    class_name=pred.class_name
                )
                proto_detections.append(proto_detection)
            
            return backend_pb2.DetectResponse(Detections=proto_detections)
        except Exception as err:
            print(f"Detection error: {err}")
            return backend_pb2.DetectResponse()

    def Status(self, request, context):
        """
        A gRPC method that returns the status of the backend service.

        Args:
            request: A HealthMessage object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A StatusResponse object that contains the status information.
        """
        state = backend_pb2.StatusResponse.READY if self.model is not None else backend_pb2.StatusResponse.UNINITIALIZED
        return backend_pb2.StatusResponse(state=state)

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
    print("[RFDETR] Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("[RFDETR] Received termination signal. Shutting down...")
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
    parser = argparse.ArgumentParser(description="Run the RFDETR gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()
    print(f"[RFDETR] startup: {args}", file=sys.stderr)
    serve(args.addr)



