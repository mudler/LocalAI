import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc

import grpc

import unittest
import subprocess
import time
import grpc
import backend_pb2_grpc
import backend_pb2

class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service.

    This class contains methods to test the startup and shutdown of the gRPC service.
    """
    def setUp(self):
        self.service = subprocess.Popen(["python", "backend.py", "--addr", "localhost:50051"])
        time.sleep(10)

    def tearDown(self) -> None:
        self.service.terminate()
        self.service.wait()

    def test_server_startup(self):
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
        This method tests if the TTS model is loaded successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Kokoro-82M-4bit"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "MLX-Audio TTS model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_tts_generation(self):
        """
        This method tests if TTS audio is generated successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Kokoro-82M-4bit"))
                self.assertTrue(response.success)
                
                # Test TTS generation
                tts_req = backend_pb2.TTSRequest(
                    text="Hello, this is a test of the MLX-Audio TTS system.",
                    model="mlx-community/Kokoro-82M-4bit",
                    voice="af_heart",
                    language="a"
                )
                tts_resp = stub.TTS(tts_req)
                self.assertTrue(tts_resp.success)
                self.assertIn("TTS audio generated successfully", tts_resp.message)
        except Exception as err:
            print(err)
            self.fail("TTS service failed")
        finally:
            self.tearDown()

    def test_tts_with_options(self):
        """
        This method tests if TTS works with various options and parameters
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(
                    Model="mlx-community/Kokoro-82M-4bit",
                    Options=["voice:af_soft", "speed:1.2", "lang_code:b"]
                ))
                self.assertTrue(response.success)
                
                # Test TTS generation with different voice and language
                tts_req = backend_pb2.TTSRequest(
                    text="Hello, this is a test with British English accent.",
                    model="mlx-community/Kokoro-82M-4bit",
                    voice="af_soft",
                    language="b"
                )
                tts_resp = stub.TTS(tts_req)
                self.assertTrue(tts_resp.success)
                self.assertIn("TTS audio generated successfully", tts_resp.message)
        except Exception as err:
            print(err)
            self.fail("TTS with options service failed")
        finally:
            self.tearDown()


    def test_tts_multilingual(self):
        """
        This method tests if TTS works with different languages
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Kokoro-82M-4bit"))
                self.assertTrue(response.success)
                
                # Test Spanish TTS
                tts_req = backend_pb2.TTSRequest(
                    text="Hola, esto es una prueba del sistema TTS MLX-Audio.",
                    model="mlx-community/Kokoro-82M-4bit",
                    voice="af_heart",
                    language="e"
                )
                tts_resp = stub.TTS(tts_req)
                self.assertTrue(tts_resp.success)
                self.assertIn("TTS audio generated successfully", tts_resp.message)
        except Exception as err:
            print(err)
            self.fail("Multilingual TTS service failed")
        finally:
            self.tearDown()