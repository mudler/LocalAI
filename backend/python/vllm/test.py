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


    def test_messages_to_dicts(self):
        """
        Tests _messages_to_dicts conversion of proto Messages to dicts.
        """
        import sys, os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        servicer = BackendServicer()
        msgs = [
            backend_pb2.Message(role="user", content="hello"),
            backend_pb2.Message(
                role="assistant",
                content="",
                tool_calls='[{"id":"call_1","type":"function","function":{"name":"foo","arguments":"{}"}}]',
                reasoning_content="thinking...",
            ),
            backend_pb2.Message(role="tool", content="result", name="foo", tool_call_id="call_1"),
        ]
        result = servicer._messages_to_dicts(msgs)
        self.assertEqual(len(result), 3)
        self.assertEqual(result[0], {"role": "user", "content": "hello"})
        self.assertEqual(result[1]["reasoning_content"], "thinking...")
        self.assertIsInstance(result[1]["tool_calls"], list)
        self.assertEqual(result[1]["tool_calls"][0]["id"], "call_1")
        self.assertEqual(result[2]["tool_call_id"], "call_1")
        self.assertEqual(result[2]["name"], "foo")

    def test_parse_options(self):
        """
        Tests _parse_options correctly parses key:value strings.
        """
        import sys, os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        servicer = BackendServicer()
        opts = servicer._parse_options([
            "tool_parser:hermes",
            "reasoning_parser:deepseek_r1",
            "invalid_no_colon",
            "key_with_colons:a:b:c",
        ])
        self.assertEqual(opts["tool_parser"], "hermes")
        self.assertEqual(opts["reasoning_parser"], "deepseek_r1")
        self.assertEqual(opts["key_with_colons"], "a:b:c")
        self.assertNotIn("invalid_no_colon", opts)

    def test_apply_engine_args_known_keys(self):
        """
        Tests _apply_engine_args overlays user-supplied JSON onto AsyncEngineArgs.
        """
        import sys, os, json as _json
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        from vllm.engine.arg_utils import AsyncEngineArgs

        servicer = BackendServicer()
        base = AsyncEngineArgs(model="facebook/opt-125m")
        extras = _json.dumps({
            "trust_remote_code": True,
            "max_num_seqs": 32,
        })
        out = servicer._apply_engine_args(base, extras)
        self.assertTrue(out.trust_remote_code)
        self.assertEqual(out.max_num_seqs, 32)
        # untouched fields preserved
        self.assertEqual(out.model, "facebook/opt-125m")

    def test_apply_engine_args_unknown_key_raises(self):
        """
        Tests _apply_engine_args rejects unknown keys with a helpful suggestion.
        """
        import sys, os, json as _json
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        from vllm.engine.arg_utils import AsyncEngineArgs

        servicer = BackendServicer()
        base = AsyncEngineArgs(model="facebook/opt-125m")
        with self.assertRaises(ValueError) as ctx:
            servicer._apply_engine_args(base, _json.dumps({"trustremotecode": True}))
        self.assertIn("trustremotecode", str(ctx.exception))
        # close-match hint for the typo
        self.assertIn("trust_remote_code", str(ctx.exception))

    def test_apply_engine_args_empty_passthrough(self):
        """
        Tests that empty engine_args returns the base unchanged.
        """
        import sys, os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        from vllm.engine.arg_utils import AsyncEngineArgs

        servicer = BackendServicer()
        base = AsyncEngineArgs(model="facebook/opt-125m")
        self.assertIs(servicer._apply_engine_args(base, ""), base)
        self.assertIs(servicer._apply_engine_args(base, None), base)

    def test_tokenize_string(self):
        """
        Tests the TokenizeString RPC returns valid tokens.
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="facebook/opt-125m"))
                self.assertTrue(response.success)
                resp = stub.TokenizeString(backend_pb2.PredictOptions(Prompt="Hello world"))
                self.assertGreater(resp.length, 0)
                self.assertEqual(len(resp.tokens), resp.length)
        except Exception as err:
            print(err)
            self.fail("TokenizeString service failed")
        finally:
            self.tearDown()

    def test_free(self):
        """
        Tests the Free RPC doesn't crash.
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="facebook/opt-125m"))
                self.assertTrue(response.success)
                free_resp = stub.Free(backend_pb2.HealthMessage())
                self.assertTrue(free_resp.success)
        except Exception as err:
            print(err)
            self.fail("Free service failed")
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