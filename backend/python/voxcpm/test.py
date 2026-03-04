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
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])
        time.sleep(30)

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self.service.terminate()
        self.service.wait()

    def test_load_model(self):
        """
        This method tests if the model is loaded successfully
        """
        try:
            self.setUp()
            print("Starting test_load_model")
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="openbmb/VoxCPM1.5"))
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
                tts_request = backend_pb2.TTSRequest(text="VoxCPM is an innovative end-to-end TTS model from ModelBest.", dst="test.wav")
                tts_response = stub.TTS(tts_request)
                self.assertIsNotNone(tts_response)
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_tts_stream(self):
        """
        This method tests if TTS streaming works correctly
        """
        try:
            self.setUp()
            print("Starting test_tts_stream")
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="openbmb/VoxCPM1.5"))
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
                
                # Test TTSStream
                tts_request = backend_pb2.TTSRequest(text="VoxCPM is an innovative end-to-end TTS model from ModelBest. This is a streaming test.", dst="test_stream.wav")
                chunks_received = 0
                total_audio_bytes = 0
                
                for reply in stub.TTSStream(tts_request):
                    # Verify that we receive audio chunks
                    if reply.audio:
                        chunks_received += 1
                        total_audio_bytes += len(reply.audio)
                        self.assertGreater(len(reply.audio), 0, "Audio chunk should not be empty")
                
                # Verify that we received multiple chunks
                self.assertGreater(chunks_received, 0, "Should receive at least one audio chunk")
                self.assertGreater(total_audio_bytes, 0, "Total audio bytes should be greater than 0")
                print(f"Received {chunks_received} chunks with {total_audio_bytes} total bytes")
        except Exception as err:
            print(err)
            self.fail("TTSStream service failed")
        finally:
            self.tearDown()
