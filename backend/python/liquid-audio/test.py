"""Smoke tests for the liquid-audio backend.

These run without contacting HuggingFace or loading model weights:
they only verify that the gRPC service starts and Health() responds.

To run an end-to-end inference test, set LIQUID_AUDIO_MODEL_ID
(e.g. "LiquidAI/LFM2.5-Audio-1.5B") in the environment — see test_inference().
"""
import os
import subprocess
import sys
import time
import unittest

import grpc

# Ensure generated protobuf stubs are importable
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import backend_pb2
import backend_pb2_grpc


class TestBackend(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        addr = os.environ.get("LIQUID_AUDIO_TEST_ADDR", "localhost:50053")
        cls.addr = addr
        cls.server = subprocess.Popen(
            [sys.executable, os.path.join(os.path.dirname(__file__), "backend.py"), "--addr", addr],
        )
        time.sleep(2)  # Give the server a moment to bind

    @classmethod
    def tearDownClass(cls):
        cls.server.terminate()
        try:
            cls.server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.server.kill()

    def _stub(self):
        channel = grpc.insecure_channel(self.addr)
        return backend_pb2_grpc.BackendStub(channel)

    def test_health(self):
        stub = self._stub()
        reply = stub.Health(backend_pb2.HealthMessage(), timeout=5)
        self.assertEqual(reply.message, b"OK")

    def test_load_finetune_mode_without_weights(self):
        """Loading in fine-tune mode should succeed without pulling model weights."""
        stub = self._stub()
        result = stub.LoadModel(
            backend_pb2.ModelOptions(
                Model="LiquidAI/LFM2.5-Audio-1.5B",
                Options=["mode:finetune"],
            ),
            timeout=10,
        )
        self.assertTrue(result.success, msg=result.message)

    @unittest.skipUnless(os.environ.get("LIQUID_AUDIO_MODEL_ID"),
                         "Set LIQUID_AUDIO_MODEL_ID to run an end-to-end inference smoke test")
    def test_inference(self):
        """End-to-end: load a real LFM2-Audio model and run one short prediction."""
        stub = self._stub()
        model_id = os.environ["LIQUID_AUDIO_MODEL_ID"]
        result = stub.LoadModel(
            backend_pb2.ModelOptions(
                Model=model_id,
                Options=["mode:chat"],
            ),
            timeout=600,
        )
        self.assertTrue(result.success, msg=result.message)
        reply = stub.Predict(
            backend_pb2.PredictOptions(
                Prompt="Hello!",
                Tokens=8,
                Temperature=0.0,
            ),
            timeout=120,
        )
        self.assertGreater(len(reply.message), 0)


if __name__ == "__main__":
    unittest.main()
