"""
Tests for the ACE-Step gRPC backend.
"""
import os
import tempfile
import unittest

import backend_pb2
import backend_pb2_grpc
import grpc


class TestACEStepBackend(unittest.TestCase):
    """Test Health, LoadModel, and SoundGeneration (minimal; no real model required)."""

    @classmethod
    def setUpClass(cls):
        port = os.environ.get("BACKEND_PORT", "50051")
        cls.channel = grpc.insecure_channel(f"localhost:{port}")
        cls.stub = backend_pb2_grpc.BackendStub(cls.channel)

    @classmethod
    def tearDownClass(cls):
        cls.channel.close()

    def test_health(self):
        response = self.stub.Health(backend_pb2.HealthMessage())
        self.assertEqual(response.message, b"OK")

    def test_load_model(self):
        response = self.stub.LoadModel(backend_pb2.ModelOptions(Model="ace-step-test"))
        self.assertTrue(response.success, response.message)

    def test_sound_generation_minimal(self):
        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as f:
            dst = f.name
        try:
            req = backend_pb2.SoundGenerationRequest(
                text="upbeat pop song",
                model="ace-step-test",
                dst=dst,
            )
            response = self.stub.SoundGeneration(req)
            self.assertTrue(response.success, response.message)
            self.assertTrue(os.path.exists(dst), f"Output file not created: {dst}")
            self.assertGreater(os.path.getsize(dst), 0)
        finally:
            if os.path.exists(dst):
                os.unlink(dst)


if __name__ == "__main__":
    unittest.main()
