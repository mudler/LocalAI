"""
Test script for the OuteTTS gRPC service.
"""
import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc

import grpc


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])
        time.sleep(5)

    def tearDown(self):
        self.service.terminate()
        self.service.wait()

    def test_health(self):
        try:
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.Health(backend_pb2.HealthMessage())
                self.assertEqual(response.message, b'OK')
        except Exception as err:
            self.fail(f"Health check failed: {err}")
        finally:
            self.tearDown()


if __name__ == "__main__":
    unittest.main()
