"""
A test script to test the gRPC service
"""
import unittest
import subprocess
import time
import os
import tempfile
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
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])
        time.sleep(30)

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self.service.terminate()
        self.service.wait()

    def test_server_startup(self):
        """
        This method tests if the server starts up successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.Health(backend_pb2.HealthMessage())
                self.assertEqual(response.message, b'OK')
        except Exception as err:
            print(err)
            self.fail("Server failed to start")
        finally:
            self.tearDown()

    def test_load_model(self):
        """
        This method tests if the model is loaded successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions())
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_tts_with_hf_voice(self):
        """
        This method tests TTS generation with HuggingFace voice URL
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Load model
                response = stub.LoadModel(backend_pb2.ModelOptions())
                self.assertTrue(response.success)
                
                # Create temporary output file
                with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp_file:
                    output_path = tmp_file.name
                
                # Test TTS with HuggingFace voice URL
                tts_request = backend_pb2.TTSRequest(
                    text="Hello world, this is a test.",
                    dst=output_path,
                    voice="azelma"
                )
                tts_response = stub.TTS(tts_request)
                self.assertTrue(tts_response.success)
                
                # Verify output file exists and is not empty
                self.assertTrue(os.path.exists(output_path))
                self.assertGreater(os.path.getsize(output_path), 0)
                
                # Cleanup
                os.unlink(output_path)
        except Exception as err:
            print(err)
            self.fail("TTS service failed")
        finally:
            self.tearDown()

    def test_tts_with_default_voice(self):
        """
        This method tests TTS generation with default voice (via AudioPath in LoadModel)
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Load model with default voice
                load_request = backend_pb2.ModelOptions(
                    Options=["default_voice:azelma"]
                )
                response = stub.LoadModel(load_request)
                self.assertTrue(response.success)
                
                # Create temporary output file
                with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp_file:
                    output_path = tmp_file.name
                
                # Test TTS without specifying voice (should use default)
                tts_request = backend_pb2.TTSRequest(
                    text="Hello world, this is a test.",
                    dst=output_path
                )
                tts_response = stub.TTS(tts_request)
                self.assertTrue(tts_response.success)
                
                # Verify output file exists and is not empty
                self.assertTrue(os.path.exists(output_path))
                self.assertGreater(os.path.getsize(output_path), 0)
                
                # Cleanup
                os.unlink(output_path)
        except Exception as err:
            print(err)
            self.fail("TTS service with default voice failed")
        finally:
            self.tearDown()
