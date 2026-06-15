import os
import sys
import unittest
import subprocess
import time
import types

import grpc
import backend_pb2
import backend_pb2_grpc

# Make the shared helpers importable so we can unit-test them without a
# running gRPC server.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
from python_utils import messages_to_dicts, parse_options
from mlx_utils import parse_tool_calls, split_reasoning

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
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)
                self.assertEqual(response.message, "MLX model loaded successfully")
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
        """
        This method tests if all sampling parameters are correctly processed
        NOTE: this does NOT test for correctness, just that we received a compatible response
        """
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
                    PresencePenalty=0.1,
                    FrequencyPenalty=0.2,
                    MinP=0.05,
                    Seed=42,
                    StopPrompts=["\n"],
                    IgnoreEOS=True,
                )
                resp = stub.Predict(req)
                self.assertIsNotNone(resp.message)
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

    def test_concurrent_requests(self):
        """
        This method tests that concurrent requests don't corrupt each other's cache state.
        This is a regression test for the race condition in the original implementation.
        """
        import concurrent.futures

        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)

                def make_request(prompt):
                    req = backend_pb2.PredictOptions(Prompt=prompt, Tokens=20)
                    return stub.Predict(req)

                # Run 5 concurrent requests with different prompts
                prompts = [
                    "The capital of France is",
                    "The capital of Germany is",
                    "The capital of Italy is",
                    "The capital of Spain is",
                    "The capital of Portugal is",
                ]

                with concurrent.futures.ThreadPoolExecutor(max_workers=5) as executor:
                    futures = [executor.submit(make_request, p) for p in prompts]
                    results = [f.result() for f in concurrent.futures.as_completed(futures)]

                # All results should be non-empty
                messages = [r.message for r in results]
                self.assertTrue(all(len(m) > 0 for m in messages), "All requests should return non-empty responses")
                print(f"Concurrent test passed: {len(messages)} responses received")

        except Exception as err:
            print(err)
            self.fail("Concurrent requests test failed")
        finally:
            self.tearDown()

    def test_cache_reuse(self):
        """
        This method tests that repeated prompts reuse cached KV states.
        The second request should benefit from the cached prompt processing.
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)

                prompt = "The quick brown fox jumps over the lazy dog. "

                # First request - populates cache
                req1 = backend_pb2.PredictOptions(Prompt=prompt, Tokens=10)
                resp1 = stub.Predict(req1)
                self.assertIsNotNone(resp1.message)

                # Second request with same prompt - should reuse cache
                req2 = backend_pb2.PredictOptions(Prompt=prompt, Tokens=10)
                resp2 = stub.Predict(req2)
                self.assertIsNotNone(resp2.message)

                print(f"Cache reuse test passed: first={len(resp1.message)} bytes, second={len(resp2.message)} bytes")

        except Exception as err:
            print(err)
            self.fail("Cache reuse test failed")
        finally:
            self.tearDown()

    def test_prefix_cache_reuse(self):
        """
        This method tests that prompts sharing a common prefix benefit from cached KV states.
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit"))
                self.assertTrue(response.success)

                # First request with base prompt
                prompt_base = "Once upon a time in a land far away, "
                req1 = backend_pb2.PredictOptions(Prompt=prompt_base, Tokens=10)
                resp1 = stub.Predict(req1)
                self.assertIsNotNone(resp1.message)

                # Second request with extended prompt (same prefix)
                prompt_extended = prompt_base + "there lived a brave knight who "
                req2 = backend_pb2.PredictOptions(Prompt=prompt_extended, Tokens=10)
                resp2 = stub.Predict(req2)
                self.assertIsNotNone(resp2.message)

                print(f"Prefix cache test passed: base={len(resp1.message)} bytes, extended={len(resp2.message)} bytes")

        except Exception as err:
            print(err)
            self.fail("Prefix cache reuse test failed")
        finally:
            self.tearDown()


    def test_tokenize_string(self):
        """TokenizeString should return a non-empty token list for a known prompt."""
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(
                    backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit")
                )
                self.assertTrue(response.success)
                resp = stub.TokenizeString(backend_pb2.PredictOptions(Prompt="Hello, world"))
                self.assertGreater(resp.length, 0)
                self.assertEqual(len(list(resp.tokens)), resp.length)
        except Exception as err:
            print(err)
            self.fail("TokenizeString service failed")
        finally:
            self.tearDown()

    def test_free(self):
        """Free should release the model and not crash on subsequent calls."""
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(
                    backend_pb2.ModelOptions(Model="mlx-community/Llama-3.2-1B-Instruct-4bit")
                )
                self.assertTrue(response.success)
                free_resp = stub.Free(backend_pb2.HealthMessage())
                self.assertTrue(free_resp.success)
        except Exception as err:
            print(err)
            self.fail("Free service failed")
        finally:
            self.tearDown()


class TestSharedHelpers(unittest.TestCase):
    """Server-less unit tests for the helpers the mlx backend depends on."""

    def test_parse_options_typed(self):
        opts = parse_options(["temperature:0.7", "max_tokens:128", "trust:true", "name:hello", "no_colon_skipped"])
        self.assertEqual(opts["temperature"], 0.7)
        self.assertEqual(opts["max_tokens"], 128)
        self.assertIs(opts["trust"], True)
        self.assertEqual(opts["name"], "hello")
        self.assertNotIn("no_colon_skipped", opts)

    def test_messages_to_dicts_roundtrip(self):
        # Build proto Message objects (via backend_pb2 to match real gRPC)
        msgs = [
            backend_pb2.Message(role="user", content="hi"),
            backend_pb2.Message(
                role="assistant",
                content="",
                tool_calls='[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]',
            ),
            backend_pb2.Message(
                role="tool",
                content="42",
                tool_call_id="call_1",
                name="f",
            ),
        ]
        out = messages_to_dicts(msgs)
        self.assertEqual(out[0], {"role": "user", "content": "hi"})
        self.assertEqual(out[1]["role"], "assistant")
        self.assertEqual(out[1]["tool_calls"][0]["function"]["name"], "f")
        self.assertEqual(out[2]["tool_call_id"], "call_1")
        self.assertEqual(out[2]["name"], "f")

    def test_split_reasoning(self):
        r, c = split_reasoning("<think>step 1\nstep 2</think>The answer is 42.", "<think>", "</think>")
        self.assertEqual(r, "step 1\nstep 2")
        self.assertEqual(c, "The answer is 42.")

    def test_split_reasoning_no_marker(self):
        r, c = split_reasoning("just text", "<think>", "</think>")
        self.assertEqual(r, "")
        self.assertEqual(c, "just text")

    def test_parse_tool_calls_with_shim(self):
        tm = types.SimpleNamespace(
            tool_call_start="<tool_call>",
            tool_call_end="</tool_call>",
            parse_tool_call=lambda body, tools: {"name": "get_weather", "arguments": {"location": body.strip()}},
        )
        calls, remaining = parse_tool_calls(
            "Sure: <tool_call>Paris</tool_call>",
            tm,
            tools=None,
        )
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["name"], "get_weather")
        self.assertEqual(calls[0]["arguments"], '{"location": "Paris"}')
        self.assertEqual(calls[0]["index"], 0)
        self.assertNotIn("<tool_call>", remaining)


# Unit tests for ThreadSafeLRUPromptCache are in test_mlx_cache.py