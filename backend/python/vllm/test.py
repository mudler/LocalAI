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


class TestStreamingToolParser(unittest.TestCase):
    """
    Server-less unit tests for the streaming + tool-parser machinery in
    BackendServicer._predict. These tests instantiate BackendServicer
    directly and mock the vLLM engine + tool parser, so they do not need
    a GPU, a model, or a running gRPC server. Kept in a separate class to
    avoid the parent setUp() which spawns a subprocess.

    Covers #582 (follow-up to #10346):
      1. Markup-leak prevention with a non-streaming parser (buffer fallback)
      2. No content duplication on the plain-text path with the buffer fallback
      3. Native streaming progressive plain-text emission
      4. Native streaming structured tool_call, no markup leak
      5. Parser exception → graceful fallback to buffer, still no markup
      6. No-tool-parser regression: unchanged per-delta content stream
    """

    @staticmethod
    def _make_generate(chunks):
        """Build a fake vLLM engine.generate that yields cumulative chunks."""
        from types import SimpleNamespace
        async def gen(*a, **k):
            for i, t in enumerate(chunks):
                yield SimpleNamespace(
                    outputs=[SimpleNamespace(
                        text=t,
                        token_ids=list(range(i + 1)),
                        logprobs=None,
                    )],
                    prompt_token_ids=[0],
                )
        return lambda *a, **k: gen()

    @staticmethod
    def _collect(servicer, req):
        import asyncio
        async def run():
            return [r async for r in servicer._predict(req, None, streaming=True)]
        return asyncio.run(run())

    def _new_servicer(self):
        import sys, os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer
        s = BackendServicer()
        s.reasoning_parser_cls = None
        s.tool_parser_cls = None
        s.tokenizer = None
        return s

    # ── Case 1+2: parser without streaming method → buffer fallback ──
    def test_buffer_path_no_markup_no_duplication(self):
        from types import SimpleNamespace

        def parser_cls(called, content_text, calls):
            class _P:
                def __init__(self, tokenizer, tools=None):
                    pass
                # NOTE: NO extract_tool_calls_streaming → takes the buffer path
                def extract_tool_calls(self, c, request=None):
                    return SimpleNamespace(
                        tools_called=called, content=content_text, tool_calls=calls,
                    )
            return _P

        tools_json = '[{"type":"function","function":{"name":"calc","parameters":{}}}]'

        # Tool-call case: no raw markup in any delta.content
        s = self._new_servicer()
        s.llm = SimpleNamespace(generate=self._make_generate([
            '<tool_call>\n{"name": "calc"',
            '<tool_call>\n{"name": "calc", "arguments": {"x": 1}}\n</tool_call>',
        ]))
        call = SimpleNamespace(id="call_1",
                               function=SimpleNamespace(name="calc", arguments='{"x": 1}'))
        s.tool_parser_cls = parser_cls(True, "", [call])
        req = backend_pb2.PredictOptions(Prompt="x", Tools=tools_json)
        replies = self._collect(s, req)
        contents = [cd.content for r in replies for cd in r.chat_deltas if cd.content]
        self.assertFalse(
            any("<tool_call" in c for c in contents),
            f"markup leaked: {contents!r}",
        )
        names = [tc.name for r in replies for cd in r.chat_deltas for tc in cd.tool_calls]
        self.assertIn("calc", names, "tool_call missing from final chunk")

        # Plain-text-with-tools case: full content delivered exactly once
        s2 = self._new_servicer()
        s2.llm = SimpleNamespace(generate=self._make_generate([
            "The capital ",
            "The capital of France is Paris.",
        ]))
        s2.tool_parser_cls = parser_cls(False, "", [])
        req2 = backend_pb2.PredictOptions(Prompt="x", Tools=tools_json)
        joined = "".join(
            cd.content for r in self._collect(s2, req2)
            for cd in r.chat_deltas if cd.content
        )
        self.assertEqual(
            joined.count("The capital of France is Paris."), 1,
            f"buffered content duplicated: {joined!r}",
        )

    # ── Case 3: native streaming, progressive plain text ──
    def test_native_streaming_progressive_plain_text(self):
        from types import SimpleNamespace

        class _DeltaMsg:
            def __init__(self, content=None, reasoning=None, tool_calls=None):
                self.content = content
                self.reasoning = reasoning
                self.tool_calls = tool_calls or []

        class StreamingParser:
            def __init__(self, tokenizer, tools=None):
                pass
            def extract_tool_calls(self, c, request=None):
                # Should NOT be called when native streaming runs successfully.
                raise AssertionError("extract_tool_calls invoked on native-streaming path")
            def extract_tool_calls_streaming(
                self, previous_text, current_text, delta_text,
                previous_token_ids, current_token_ids, delta_token_ids, request,
            ):
                if not delta_text:
                    return None
                return _DeltaMsg(content=delta_text)

        s = self._new_servicer()
        s.llm = SimpleNamespace(generate=self._make_generate([
            "Paris ",
            "Paris is ",
            "Paris is the capital of France.",
        ]))
        s.tool_parser_cls = StreamingParser
        req = backend_pb2.PredictOptions(
            Prompt="x",
            Tools='[{"type":"function","function":{"name":"calc","parameters":{}}}]',
        )
        replies = self._collect(s, req)

        intermediate_content = [
            cd.content for r in replies[:-1] for cd in r.chat_deltas if cd.content
        ]
        self.assertTrue(
            len(intermediate_content) > 0,
            "Plain-text response not streamed progressively (native streaming inactive?)",
        )
        assembled = "".join(
            cd.content for r in replies for cd in r.chat_deltas if cd.content
        )
        self.assertEqual(
            assembled, "Paris is the capital of France.",
            f"Assembled content wrong: {assembled!r}",
        )

    # ── Case 4: native streaming, structured tool_call, no markup ──
    def test_native_streaming_tool_call_no_markup_leak(self):
        from types import SimpleNamespace

        class _DeltaMsg:
            def __init__(self, content=None, reasoning=None, tool_calls=None):
                self.content = content
                self.reasoning = reasoning
                self.tool_calls = tool_calls or []

        class _ToolCallStreamer:
            def __init__(self, tokenizer, tools=None):
                self._emitted = False
            def extract_tool_calls(self, c, request=None):
                raise AssertionError("extract_tool_calls invoked on native-streaming path")
            def extract_tool_calls_streaming(
                self, previous_text, current_text, delta_text,
                previous_token_ids, current_token_ids, delta_token_ids, request,
            ):
                if "</tool_call>" in current_text and not self._emitted:
                    self._emitted = True
                    fn = SimpleNamespace(name="calc", arguments='{"x": 1}')
                    tc = SimpleNamespace(id="call_1", type="function", index=0, function=fn)
                    return _DeltaMsg(tool_calls=[tc])
                return None

        s = self._new_servicer()
        s.llm = SimpleNamespace(generate=self._make_generate([
            '<tool_call>\n',
            '<tool_call>\n{"name": "calc"',
            '<tool_call>\n{"name": "calc", "arguments": {"x": 1}}\n</tool_call>',
        ]))
        s.tool_parser_cls = _ToolCallStreamer
        req = backend_pb2.PredictOptions(
            Prompt="x",
            Tools='[{"type":"function","function":{"name":"calc","parameters":{}}}]',
        )
        replies = self._collect(s, req)

        contents = [cd.content for r in replies for cd in r.chat_deltas if cd.content]
        self.assertFalse(
            any("<tool_call" in c or "</tool_call>" in c for c in contents),
            f"markup leaked as content: {contents!r}",
        )
        names = [tc.name for r in replies for cd in r.chat_deltas for tc in cd.tool_calls if tc.name]
        args  = [tc.arguments for r in replies for cd in r.chat_deltas for tc in cd.tool_calls if tc.arguments]
        self.assertIn("calc", names, f"tool_call name missing; got {names!r}")
        self.assertIn('{"x": 1}', args, f"tool_call args missing; got {args!r}")

    # ── Case 5: parser exception → fallback to buffer, no leak ──
    def test_native_streaming_parser_exception_falls_back_to_buffer(self):
        from types import SimpleNamespace
        call = SimpleNamespace(id="call_1",
                               function=SimpleNamespace(name="calc", arguments='{"x": 1}'))

        class _BrokenStreamer:
            def __init__(self, tokenizer, tools=None):
                pass
            def extract_tool_calls(self, c, request=None):
                return SimpleNamespace(tools_called=True, content="", tool_calls=[call])
            def extract_tool_calls_streaming(self, *a, **kw):
                raise RuntimeError("simulated parser bug")

        s = self._new_servicer()
        s.llm = SimpleNamespace(generate=self._make_generate([
            '<tool_call>\n{"name": "calc"',
            '<tool_call>\n{"name": "calc", "arguments": {"x": 1}}\n</tool_call>',
        ]))
        s.tool_parser_cls = _BrokenStreamer
        req = backend_pb2.PredictOptions(
            Prompt="x",
            Tools='[{"type":"function","function":{"name":"calc","parameters":{}}}]',
        )
        replies = self._collect(s, req)

        contents = [cd.content for r in replies for cd in r.chat_deltas if cd.content]
        self.assertFalse(
            any("<tool_call" in c for c in contents),
            f"markup leaked after parser exception: {contents!r}",
        )
        names = [tc.name for r in replies for cd in r.chat_deltas for tc in cd.tool_calls]
        self.assertIn("calc", names, "tool_call missing from final chunk after fallback")

    # ── Case 6: no tool parser → unchanged per-delta content stream ──
    def test_no_tool_parser_unchanged_per_delta_stream(self):
        from types import SimpleNamespace
        s = self._new_servicer()  # tool_parser_cls already None
        s.llm = SimpleNamespace(generate=self._make_generate([
            "Hello ", "Hello world", "Hello world!",
        ]))
        req = backend_pb2.PredictOptions(Prompt="x", Tools="")
        replies = self._collect(s, req)

        intermediate = [
            cd.content for r in replies[:-1] for cd in r.chat_deltas if cd.content
        ]
        self.assertEqual(
            intermediate, ["Hello ", "world", "!"],
            f"plain streaming changed; got {intermediate!r}",
        )
