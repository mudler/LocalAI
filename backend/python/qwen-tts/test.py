"""
A test script to test the gRPC service
"""
import unittest
import subprocess
import time
import os
import sys
import tempfile
import threading
import backend_pb2
import backend_pb2_grpc

import grpc


class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service
    """
    def setUp(self):
        """
        This method sets up the gRPC service by starting the server
        """
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50051"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        time.sleep(5)

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self.service.terminate()
        try:
            stdout, stderr = self.service.communicate(timeout=5)
            # Output should already be printed by threads, but print any remaining
            if stdout:
                print("=== REMAINING STDOUT ===")
                print(stdout)
            if stderr:
                print("=== REMAINING STDERR ===")
                print(stderr)
        except subprocess.TimeoutExpired:
            self.service.kill()
            stdout, stderr = self.service.communicate()
            if stdout:
                print("=== REMAINING STDOUT ===")
                print(stdout)
            if stderr:
                print("=== REMAINING STDERR ===")
                print(stderr)

    def test_tts(self):
        """
        This method tests if the TTS generation works successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Allow up to 10 minutes for model download on first run
                response = stub.LoadModel(
                    backend_pb2.ModelOptions(Model="Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice"),
                    timeout=600.0
                )
                self.assertTrue(response.success)
                
                # Create temporary output file
                with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp_file:
                    output_path = tmp_file.name
                
                tts_request = backend_pb2.TTSRequest(
                    text="Hello, this is a test of the qwen-tts backend.",
                    voice="Vivian",
                    dst=output_path
                )
                # Allow up to 2 minutes for TTS generation
                tts_response = stub.TTS(tts_request, timeout=120.0)
                self.assertIsNotNone(tts_response)
                self.assertTrue(tts_response.success)
                
                # Verify output file exists and is not empty
                self.assertTrue(os.path.exists(output_path))
                self.assertGreater(os.path.getsize(output_path), 0)
                
                # Cleanup
                os.unlink(output_path)
        except Exception as err:
            print(f"Exception: {err}", file=sys.stderr)
            # Give threads a moment to flush any remaining output
            time.sleep(1)
            self.fail("TTS service failed")
        finally:
            self.tearDown()
