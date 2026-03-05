import unittest
import subprocess
import time

import grpc
import backend_pb2
import backend_pb2_grpc


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(
            ["python", "backend.py", "--addr", "localhost:50051",
             "--hostfile", "/dev/null", "--rank", "0"]
        )
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
