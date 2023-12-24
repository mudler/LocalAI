"""
A test script to test the gRPC service
"""
import unittest
import subprocess
import time
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
        self.service = subprocess.Popen(["python3", "coqui_server.py", "--addr", "localhost:50051"])
        time.sleep(10)

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
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="tts_models/en/vctk/vits"))
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_tts(self):
        """
        This method tests if the embeddings are generated successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="tts_models/en/vctk/vits"))
                self.assertTrue(response.success)
                tts_request = backend_pb2.TTSRequest(text="80s TV news production music hit for tonight's biggest story")
                tts_response = stub.TTS(tts_request)
                self.assertIsNotNone(tts_response)
        except Exception as err:
            print(err)
            self.fail("TTS service failed")
        finally:
            self.tearDown()