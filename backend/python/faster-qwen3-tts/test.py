"""
Tests for the faster-qwen3-tts gRPC backend.
"""
import unittest
import subprocess
import time
import os
import sys
import tempfile
import backend_pb2
import backend_pb2_grpc
import grpc


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50052"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )
        time.sleep(15)

    def tearDown(self):
        self.service.terminate()
        try:
            self.service.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            self.service.kill()
            self.service.communicate()

    def test_health(self):
        with grpc.insecure_channel("localhost:50052") as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            reply = stub.Health(backend_pb2.HealthMessage(), timeout=5.0)
        self.assertEqual(reply.message, b"OK")

    def test_load_model_requires_cuda(self):
        with grpc.insecure_channel("localhost:50052") as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            response = stub.LoadModel(
                backend_pb2.ModelOptions(
                    Model="Qwen/Qwen3-TTS-12Hz-0.6B-Base",
                    CUDA=True,
                ),
                timeout=10.0,
            )
        self.assertFalse(response.success)

    @unittest.skipUnless(
        __import__("torch").cuda.is_available(),
        "faster-qwen3-tts TTS requires CUDA",
    )
    def test_tts(self):
        import soundfile as sf
        try:
            with grpc.insecure_channel("localhost:50052") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                ref_audio = tempfile.NamedTemporaryFile(suffix='.wav', delete=False)
                ref_audio.close()
                try:
                    sr = 22050
                    duration = 1.0
                    samples = int(sr * duration)
                    sf.write(ref_audio.name, [0.0] * samples, sr)

                    response = stub.LoadModel(
                        backend_pb2.ModelOptions(
                            Model="Qwen/Qwen3-TTS-12Hz-0.6B-Base",
                            AudioPath=ref_audio.name,
                            Options=["ref_text:Hello world"],
                        ),
                        timeout=600.0,
                    )
                    self.assertTrue(response.success, response.message)

                    with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as out:
                        output_path = out.name
                    try:
                        tts_response = stub.TTS(
                            backend_pb2.TTSRequest(
                                text="Test output.",
                                dst=output_path,
                                language="English",
                            ),
                            timeout=120.0,
                        )
                        self.assertTrue(tts_response.success, tts_response.message)
                        self.assertTrue(os.path.exists(output_path))
                        self.assertGreater(os.path.getsize(output_path), 0)
                    finally:
                        if os.path.exists(output_path):
                            os.unlink(output_path)
                finally:
                    if os.path.exists(ref_audio.name):
                        os.unlink(ref_audio.name)
        except Exception as err:
            self.fail(f"TTS test failed: {err}")


if __name__ == "__main__":
    unittest.main()
