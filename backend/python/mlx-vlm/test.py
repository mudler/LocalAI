import unittest
import subprocess
import time
import backend_pb2
import backend_pb2_grpc

import grpc

import unittest
import subprocess
import time
import grpc
import backend_pb2_grpc
import backend_pb2

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
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="facebook/opt-125m"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_text(self):
        """
        This method tests if the embeddings are generated successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="facebook/opt-125m"))
                self.assertTrue(response.success)
                req = backend_pb2.PredictOptions(Prompt="The capital of France is")
                resp = stub.Predict(req)
                self.assertIsNotNone(resp.message)
        except Exception as err:
            print(err)
            self.fail("text service failed")
        finally:
            self.tearDown()

    def test_sampling_params(self):
        """
        This method tests if all sampling parameters are correctly processed
        NOTE: this does NOT test for correctness, just that we received a compatible response
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="facebook/opt-125m"))
                self.assertTrue(response.success)

                req = backend_pb2.PredictOptions(
                    Prompt="The capital of France is",
                    TopP=0.8,
                    Tokens=50,
                    Temperature=0.7,
                    TopK=40,
                    PresencePenalty=0.1,
                    FrequencyPenalty=0.2,
                    RepetitionPenalty=1.1,
                    MinP=0.05,
                    Seed=42,
                    StopPrompts=["\n"],
                    StopTokenIds=[50256],
                    BadWords=["badword"],
                    IncludeStopStrInOutput=True,
                    IgnoreEOS=True,
                    MinTokens=5,
                    Logprobs=5,
                    PromptLogprobs=5,
                    SkipSpecialTokens=True,
                    SpacesBetweenSpecialTokens=True,
                    TruncatePromptTokens=10,
                    GuidedDecoding=True,
                    N=2,
                )
                resp = stub.Predict(req)
                self.assertIsNotNone(resp.message)
                self.assertIsNotNone(resp.logprobs)
        except Exception as err:
            print(err)
            self.fail("sampling params service failed")
        finally:
            self.tearDown()


    def test_embedding(self):
        """
        This method tests if the embeddings are generated successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="intfloat/e5-mistral-7b-instruct"))
                self.assertTrue(response.success)
                embedding_request = backend_pb2.PredictOptions(Embeddings="This is a test sentence.")
                embedding_response = stub.Embedding(embedding_request)
                self.assertIsNotNone(embedding_response.embeddings)
                # assert that is a list of floats
                self.assertIsInstance(embedding_response.embeddings, list)
                # assert that the list is not empty
                self.assertTrue(len(embedding_response.embeddings) > 0)
        except Exception as err:
            print(err)
            self.fail("Embedding service failed")
        finally:
            self.tearDown()