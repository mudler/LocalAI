"""
A test script to test the gRPC service
"""
import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc

import grpc


class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service
    """
    def setUp(self):
        """
        This method sets up the gRPC service by starting the server
        """
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self.service.kill()
        self.service.wait()

    def test_server_startup(self):
        """
        This method tests if the server starts up successfully
        """
        time.sleep(10)
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
        time.sleep(10)
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="bert-base-cased"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_embedding(self):
        """
        This method tests if the embeddings are generated successfully
        """
        time.sleep(10)
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="bert-base-cased"))
                print(response.message)
                self.assertTrue(response.success)
                embedding_request = backend_pb2.PredictOptions(Embeddings="This is a test sentence.")
                embedding_response = stub.Embedding(embedding_request)
                self.assertIsNotNone(embedding_response.embeddings)
        except Exception as err:
            print(err)
            self.fail("Embedding service failed")
        finally:
            self.tearDown()