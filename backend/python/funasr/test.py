"""
Tests for the FunASR gRPC backend.
"""
import unittest
import subprocess
import time
import os
import tempfile
import shutil
import backend_pb2
import backend_pb2_grpc

import grpc

SKIP_ASR_TESTS = os.environ.get("SKIP_ASR_TESTS", "false").lower() == "true"


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])
        time.sleep(15)

    def tearDown(self):
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
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="iic/SenseVoiceSmall"))
                self.assertTrue(response.success, response.message)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    @unittest.skipIf(SKIP_ASR_TESTS, "ASR transcription test skipped (SKIP_ASR_TESTS=true)")
    def test_audio_transcription(self):
        temp_dir = tempfile.mkdtemp()
        audio_file = os.path.join(temp_dir, 'audio.wav')
        try:
            url = "https://isv-data.oss-cn-hangzhou.aliyuncs.com/ics/MaaS/ASR/test_audio/BAC009S0764W0121.wav"
            result = subprocess.run(
                ["wget", "-q", url, "-O", audio_file],
                capture_output=True,
                text=True,
                timeout=30,
            )
            if result.returncode != 0:
                self.skipTest(f"Could not download sample audio: {result.stderr}")
            if not os.path.exists(audio_file):
                self.skipTest("Sample audio file not found after download")

            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                load_response = stub.LoadModel(backend_pb2.ModelOptions(Model="iic/SenseVoiceSmall"))
                self.assertTrue(load_response.success, load_response.message)

                transcript_response = stub.AudioTranscription(
                    backend_pb2.TranscriptRequest(dst=audio_file)
                )
                self.assertIsNotNone(transcript_response)
                self.assertIsNotNone(transcript_response.text)
                self.assertGreater(len(transcript_response.text), 0)
                print(f"Transcription: {transcript_response.text}")
        finally:
            self.tearDown()
            if os.path.exists(temp_dir):
                shutil.rmtree(temp_dir)
