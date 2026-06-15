"""
Test script for the TRL fine-tuning gRPC backend.
"""
import unittest
import subprocess
import time

import grpc
import backend_pb2
import backend_pb2_grpc


class TestBackendServicer(unittest.TestCase):
    """Tests for the TRL fine-tuning gRPC service."""

    def setUp(self):
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50051"]
        )
        time.sleep(10)

    def tearDown(self):
        self.service.kill()
        self.service.wait()

    def test_server_startup(self):
        """Test that the server starts and responds to health checks."""
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

    def test_list_checkpoints_empty(self):
        """Test listing checkpoints on a non-existent directory."""
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.ListCheckpoints(
                    backend_pb2.ListCheckpointsRequest(output_dir="/nonexistent")
                )
                self.assertEqual(len(response.checkpoints), 0)
        except Exception as err:
            print(err)
            self.fail("ListCheckpoints service failed")
        finally:
            self.tearDown()


if __name__ == '__main__':
    unittest.main()
