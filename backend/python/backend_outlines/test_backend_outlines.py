import unittest
import grpc
import backend_pb2
import backend_pb2_grpc

class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service.

    This class contains methods to test the Health, LoadModel and Predict methods of the gRPC service.
    """

    def setUp(self) -> None:
        # Make sure the server is running before you run the tests
        self.channel = grpc.insecure_channel("localhost:50051")
        self.stub = backend_pb2_grpc.BackendStub(self.channel)

    def test_health(self):
        self.setUp()
        response = self.stub.Health(backend_pb2.HealthMessage())
        self.assertEqual(response.message, b'OK')

    def test_load_model(self):
        self.setUp()
        response = self.stub.LoadModel(backend_pb2.ModelOptions(Model=""))
        self.assertEqual(response.success, False)
        self.assertEqual(response.message, "Model name is empty")

        response = self.stub.LoadModel(backend_pb2.ModelOptions(Model="non_existent_model"))
        self.assertEqual(response.success, False)

        response = self.stub.LoadModel(backend_pb2.ModelOptions(Model="gpt2"))
        self.assertEqual(response.success, True)
        self.assertEqual(response.message, "Model loaded successfully")

    def test_predict(self):
        self.setUp()
        response = self.stub.Predict(backend_pb2.PredictOptions(Prompt="Tell me a one-sentence joke."))
        self.assertIsInstance(response.message, bytes)

if __name__ == '__main__':
    unittest.main()