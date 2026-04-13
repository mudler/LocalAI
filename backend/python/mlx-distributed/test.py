import os
import sys
import types
import unittest
import subprocess
import time

import grpc
import backend_pb2
import backend_pb2_grpc

# Make the shared helpers importable so we can unit-test them without a
# running gRPC server.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
from python_utils import messages_to_dicts, parse_options
from mlx_utils import parse_tool_calls, split_reasoning


class TestBackendServicer(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(
            ["python", "backend.py", "--addr", "localhost:50051"]
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

    def test_load_model(self):
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_text(self):
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
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
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)

                req = backend_pb2.PredictOptions(
                    Prompt="The capital of France is",
                    TopP=0.8,
                    Tokens=50,
                    Temperature=0.7,
                    TopK=40,
                    MinP=0.05,
                    Seed=42,
                )
                resp = stub.Predict(req)
                self.assertIsNotNone(resp.message)
        except Exception as err:
            print(err)
            self.fail("sampling params service failed")
        finally:
            self.tearDown()


class TestSharedHelpers(unittest.TestCase):
    """Server-less unit tests for the helpers the mlx-distributed backend depends on."""

    def test_parse_options_typed(self):
        opts = parse_options(["temperature:0.7", "max_tokens:128", "trust:true"])
        self.assertEqual(opts["temperature"], 0.7)
        self.assertEqual(opts["max_tokens"], 128)
        self.assertIs(opts["trust"], True)

    def test_messages_to_dicts_roundtrip(self):
        msgs = [
            backend_pb2.Message(role="user", content="hi"),
            backend_pb2.Message(
                role="assistant",
                content="",
                tool_calls='[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]',
            ),
            backend_pb2.Message(role="tool", content="42", tool_call_id="call_1", name="f"),
        ]
        out = messages_to_dicts(msgs)
        self.assertEqual(out[0], {"role": "user", "content": "hi"})
        self.assertEqual(out[1]["tool_calls"][0]["function"]["name"], "f")
        self.assertEqual(out[2]["tool_call_id"], "call_1")

    def test_split_reasoning(self):
        r, c = split_reasoning("<think>plan</think>final", "<think>", "</think>")
        self.assertEqual(r, "plan")
        self.assertEqual(c, "final")

    def test_parse_tool_calls_with_shim(self):
        tm = types.SimpleNamespace(
            tool_call_start="<tool_call>",
            tool_call_end="</tool_call>",
            parse_tool_call=lambda body, tools: {"name": "get_weather", "arguments": {"location": body.strip()}},
        )
        calls, remaining = parse_tool_calls("<tool_call>Paris</tool_call>", tm, tools=None)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["name"], "get_weather")
        self.assertEqual(calls[0]["arguments"], '{"location": "Paris"}')
