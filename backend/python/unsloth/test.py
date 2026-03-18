"""Unit tests for the Unsloth fine-tuning backend."""
import unittest
import sys
import os

import backend_pb2
import backend_pb2_grpc
from backend import BackendServicer, detect_hardware


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.servicer = BackendServicer()

    def test_health(self):
        request = backend_pb2.HealthMessage()
        resp = self.servicer.Health(request, None)
        self.assertEqual(resp.message, b"OK")

    def test_load_model(self):
        request = backend_pb2.ModelOptions()
        resp = self.servicer.LoadModel(request, None)
        self.assertTrue(resp.success)

    def test_detect_hardware(self):
        hw = detect_hardware()
        self.assertIn("device", hw)
        self.assertIn(hw["device"], ("cuda", "mps", "cpu"))


if __name__ == "__main__":
    unittest.main()
