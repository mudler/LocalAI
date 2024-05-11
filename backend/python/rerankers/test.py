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
        time.sleep(10)

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
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="cross-encoder"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_rerank(self):
        """
        This method tests if the embeddings are generated successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                request = backend_pb2.RerankRequest(
                    query="I love you",
                    documents=["I hate you", "I really like you"],
                    top_n=2
                )
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="cross-encoder"))
                self.assertTrue(response.success)
               
                rerank_response = stub.Rerank(request)
                print(rerank_response.results[0])
                self.assertIsNotNone(rerank_response.results)
                self.assertEqual(len(rerank_response.results), 2)
                self.assertEqual(rerank_response.results[0].text, "I really like you")
                self.assertEqual(rerank_response.results[1].text, "I hate you")
        except Exception as err:
            print(err)
            self.fail("Reranker service failed")
        finally:
            self.tearDown()