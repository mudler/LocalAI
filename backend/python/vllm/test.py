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

    def test_streaming_tool_parser_buffering(self):
        """
        When a tool parser is active and the request carries tools, streaming
        must NOT emit the model's raw tool-call markup as content, and must NOT
        duplicate the buffered content. Exercises _predict(streaming=True) with a
        mocked engine + tool parser (no server / GPU).
        """
        import sys, os, asyncio
        from types import SimpleNamespace
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer

        def make_generate(chunks):
            async def gen(*a, **k):
                for t in chunks:
                    yield SimpleNamespace(
                        outputs=[SimpleNamespace(text=t, token_ids=[1], logprobs=None)],
                        prompt_token_ids=[0],
                    )
            return lambda *a, **k: gen()

        def parser_cls(called, content, calls):
            class _P:
                def __init__(self, tokenizer, tools=None):
                    pass
                def extract_tool_calls(self, c, request=None):
                    return SimpleNamespace(tools_called=called, content=content, tool_calls=calls)
            return _P

        def collect(servicer, req):
            async def run():
                return [r async for r in servicer._predict(req, None, streaming=True)]
            return asyncio.run(run())

        def contents(replies):
            return [cd.content for r in replies for cd in r.chat_deltas if cd.content]

        tools_json = '[{"type":"function","function":{"name":"calc"}}]'

        # Case 1: model emits a tool call -> no raw markup as content, tool_call present.
        s = BackendServicer()
        s.reasoning_parser_cls = None
        s.tokenizer = None
        s.llm = SimpleNamespace(generate=make_generate([
            '<tool_call>\n{"name": "calc"',
            '<tool_call>\n{"name": "calc", "arguments": {"x": 1}}\n</tool_call>',
        ]))
        call = SimpleNamespace(id="call_1", function=SimpleNamespace(name="calc", arguments='{"x": 1}'))
        s.tool_parser_cls = parser_cls(True, "", [call])
        req = backend_pb2.PredictOptions(Prompt="x", Tools=tools_json)
        replies = collect(s, req)
        self.assertFalse(
            any("<tool_call" in c or "<function" in c for c in contents(replies)),
            "raw tool-call markup leaked as streamed content",
        )
        names = [tc.name for r in replies for cd in r.chat_deltas for tc in cd.tool_calls]
        self.assertIn("calc", names, "structured tool_call was not emitted")

        # Case 2: tools offered but model answers in plain text -> content once.
        s2 = BackendServicer()
        s2.reasoning_parser_cls = None
        s2.tokenizer = None
        s2.llm = SimpleNamespace(generate=make_generate([
            "The capital ",
            "The capital of France is Paris.",
        ]))
        s2.tool_parser_cls = parser_cls(False, "", [])
        req2 = backend_pb2.PredictOptions(Prompt="x", Tools=tools_json)
        joined = "".join(contents(collect(s2, req2)))
        self.assertEqual(
            joined.count("The capital of France is Paris."), 1,
            f"buffered content was duplicated: {joined!r}",
        )
    @unittest.expectedFailure
    def test_streaming_tool_parser_progressive_plain_text(self):
        """
        Case 3 (TDD — currently fails, defines acceptance criterion for follow-up, see #582):

        When a tool parser is active but the model returns plain text (no tool call),
        and the parser implements extract_tool_calls_streaming, tokens should be emitted
        progressively — not held until the final chunk.

        Proposed interface:
            extract_tool_calls_streaming(delta: str, request=None)
            -> SimpleNamespace(is_tool_call_token=bool, content=str)

        Fails on current code because has_tool_parser=True suppresses all intermediate
        deltas. Will pass after Option A or B from the follow-up (Issue #582).
        """
        import sys, os, asyncio
        from types import SimpleNamespace
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer

        def make_generate(chunks):
            async def gen(*a, **k):
                for t in chunks:
                    yield SimpleNamespace(
                        outputs=[SimpleNamespace(text=t, token_ids=[1], logprobs=None)],
                        prompt_token_ids=[0],
                    )
            return lambda *a, **k: gen()

        class StreamingAwareParser:
            def __init__(self, tokenizer, tools=None):
                pass

            def extract_tool_calls(self, c, request=None):
                return SimpleNamespace(tools_called=False, content=c, tool_calls=[])

            def extract_tool_calls_streaming(self, delta, request=None):
                # Plain-text delta — not tool-call markup, pass through as content.
                return SimpleNamespace(is_tool_call_token=False, content=delta)

        def collect(servicer, req):
            async def run():
                return [r async for r in servicer._predict(req, None, streaming=True)]
            return asyncio.run(run())

        # vLLM yields cumulative text per iteration
        s = BackendServicer()
        s.reasoning_parser_cls = None
        s.tokenizer = None
        s.llm = SimpleNamespace(generate=make_generate([
            "Paris ",
            "Paris is ",
            "Paris is the capital of France.",
        ]))
        s.tool_parser_cls = StreamingAwareParser

        tools_json = '[{"type":"function","function":{"name":"calc"}}]'
        req = backend_pb2.PredictOptions(Prompt="x", Tools=tools_json)
        replies = collect(s, req)

        # Key assertion: intermediate replies (before the final chunk) must carry content.
        # Currently fails because has_tool_parser=True suppresses all intermediate deltas.
        intermediate_content = [
            cd.content
            for r in replies[:-1]
            for cd in r.chat_deltas
            if cd.content
        ]
        self.assertTrue(
            len(intermediate_content) > 0,
            "Plain-text response not streamed progressively — "
            "all content arrived in the final chunk. "
            "Fix: use extract_tool_calls_streaming when available (Option A).",
        )

        # No duplication: assembled content equals the full text exactly once.
        assembled = "".join(
            cd.content for r in replies for cd in r.chat_deltas if cd.content
        )
        self.assertEqual(
            assembled, "Paris is the capital of France.",
            f"Content wrong or duplicated: {assembled!r}",
        )