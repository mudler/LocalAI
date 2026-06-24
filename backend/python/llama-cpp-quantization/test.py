"""
Test script for the llama-cpp-quantization gRPC backend.

Downloads a small model (functiongemma-270m-it), converts it to GGUF,
and quantizes it to q4_k_m.
"""
import os
import shutil
import subprocess
import tempfile
import time
import unittest

import grpc
import backend_pb2
import backend_pb2_grpc


SERVER_ADDR = "localhost:50051"
# Small model for CI testing (~540MB)
TEST_MODEL = "unsloth/functiongemma-270m-it"


class TestQuantizationBackend(unittest.TestCase):
    """Tests for the llama-cpp-quantization gRPC service."""

    @classmethod
    def setUpClass(cls):
        cls.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", SERVER_ADDR]
        )
        time.sleep(5)
        cls.output_dir = tempfile.mkdtemp(prefix="quantize-test-")

    @classmethod
    def tearDownClass(cls):
        cls.service.kill()
        cls.service.wait()
        # Clean up output directory
        if os.path.isdir(cls.output_dir):
            shutil.rmtree(cls.output_dir, ignore_errors=True)

    def _channel(self):
        return grpc.insecure_channel(SERVER_ADDR)

    def test_01_health(self):
        """Test that the server starts and responds to health checks."""
        with self._channel() as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            response = stub.Health(backend_pb2.HealthMessage())
            self.assertEqual(response.message, b"OK")

    def test_02_quantize_small_model(self):
        """Download, convert, and quantize functiongemma-270m-it to q4_k_m."""
        with self._channel() as channel:
            stub = backend_pb2_grpc.BackendStub(channel)

            job_id = "test-quantize-001"

            # Start quantization
            result = stub.StartQuantization(
                backend_pb2.QuantizationRequest(
                    model=TEST_MODEL,
                    quantization_type="q4_k_m",
                    output_dir=self.output_dir,
                    job_id=job_id,
                )
            )
            self.assertTrue(result.success, f"StartQuantization failed: {result.message}")
            self.assertEqual(result.job_id, job_id)

            # Stream progress until completion
            final_status = None
            output_file = None
            for update in stub.QuantizationProgress(
                backend_pb2.QuantizationProgressRequest(job_id=job_id)
            ):
                print(f"  [{update.status}] {update.progress_percent:.1f}% - {update.message}")
                final_status = update.status
                if update.output_file:
                    output_file = update.output_file

            self.assertEqual(final_status, "completed", f"Expected completed, got {final_status}")
            self.assertIsNotNone(output_file, "No output_file in progress updates")
            self.assertTrue(os.path.isfile(output_file), f"Output file not found: {output_file}")

            # Verify the output is a valid GGUF file (starts with "GGUF" magic)
            with open(output_file, "rb") as f:
                magic = f.read(4)
            self.assertEqual(magic, b"GGUF", f"Output file does not have GGUF magic: {magic!r}")

            # Verify reasonable file size (q4_k_m of 270M model should be ~150-400MB)
            size_mb = os.path.getsize(output_file) / (1024 * 1024)
            print(f"  Output file size: {size_mb:.1f} MB")
            self.assertGreater(size_mb, 10, "Output file suspiciously small")


if __name__ == "__main__":
    unittest.main()
