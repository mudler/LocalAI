import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc

import grpc

class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service.

    This class contains methods to test the startup and shutdown of the gRPC service.
    """
    def setUp(self):
        self.service = subprocess.Popen(["python", "backend.py", "--addr", "localhost:50051"])
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

    def test_load_model(self):
        """
        This method tests if the model is loaded successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Use a small image generation model for testing
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="Tongyi-MAI/Z-Image-Turbo"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_generate_image(self):
        """
        This method tests if image generation works
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="Tongyi-MAI/Z-Image-Turbo"))
                self.assertTrue(response.success)
                
                req = backend_pb2.GenerateImageRequest(
                    positive_prompt="a cup of coffee on the table",
                    dst="/tmp/test_output.png",
                    width=512,
                    height=512,
                    step=20,
                    seed=42additional_information
                )
                resp = stub.GenerateImage(req)
                self.assertTrue(resp.success)
        except Exception as err:
            print(err)
            self.fail("GenerateImage service failed")
        finally:
            self.tearDown()
additional_information
if __name__ == "__main__":
    unittest.main()
