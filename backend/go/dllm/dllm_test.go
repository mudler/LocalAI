package main

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func TestDllm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dllm Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the
// C-ABI bridge without spinning up the gRPC server. The library path comes
// from DLLM_TEST_LIBRARY (gated specs Skip when it is unset).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libLoadErr = loadCAPI(os.Getenv("DLLM_TEST_LIBRARY"))
	})
}

// C-ABI binding smoke: drives the real libdllm.so against the tiny GGUF
// fixture from dllm.cpp (tests/fixtures/tiny_with_vocab.gguf). Gated on:
//
//	DLLM_TEST_LIBRARY   absolute path to libdllm.so
//	DLLM_TEST_TINY_MODEL absolute path to tiny_with_vocab.gguf
var _ = Describe("C-ABI binding", func() {
	BeforeEach(func() {
		if os.Getenv("DLLM_TEST_LIBRARY") == "" || os.Getenv("DLLM_TEST_TINY_MODEL") == "" {
			Skip("set DLLM_TEST_LIBRARY and DLLM_TEST_TINY_MODEL to run the C-ABI binding smoke")
		}
		ensureLibLoaded()
		Expect(libLoadErr).ToNot(HaveOccurred())
	})

	It("binds the 9 symbols and round-trips the tiny model", func() {
		Expect(cAbiVersion()).To(Equal(int32(1)))

		h := cLoad(os.Getenv("DLLM_TEST_TINY_MODEL"), "{}")
		Expect(h).ToNot(BeZero(), "dllm_capi_load of the tiny fixture")

		// Tiny fixture vocab: "hello" tokenizes to ids [2,18] (bos prepended
		// by the C side: vocab.add_bos).
		toks, err := cTokenizeJSON(h, "hello")
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal("[2,18]"))

		// Deterministic generation: an explicit non-negative seed seeds
		// mt19937, so two identical calls must produce identical text.
		out1, err := cGenerate(h, "hello", `{"n_predict":16,"seed":7}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(out1).ToNot(BeEmpty())
		// Cancel with no call in flight is dropped: each generate resets the
		// cancel flag on entry (header contract), so this must not affect
		// the next call. Also binds the 9th symbol; safe on NULL too.
		cCancel(h)
		cCancel(0)

		out2, err := cGenerate(h, "hello", `{"n_predict":16,"seed":7}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(out2).To(Equal(out1))

		// Streaming variant: same opts, blocks arrive via the purego
		// callback trampoline. The per-block detokenize can differ from the
		// seamless full-text decode at block boundaries, so only assert that
		// blocks arrived and were non-trivial, not byte equality with out1.
		var blocks []string
		var steps int
		err = cGenerateStream(h, "hello", `{"n_predict":16,"seed":7}`,
			func(text string) { blocks = append(blocks, text) },
			func(step, total int, preview string) { steps++ },
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(blocks).ToNot(BeEmpty())
		Expect(steps).To(BeNumerically(">", 0))

		// Load failure path: NULL ctx back, and last_error(NULL) returns the
		// static NULL-ctx message (there is no ctx to carry the real reason).
		bad := cLoad("/nonexistent/dllm-model.gguf", "{}")
		Expect(bad).To(BeZero())
		Expect(cLastError(0)).ToNot(BeEmpty())

		// Free is safe on a live handle and a NULL one (delete nullptr).
		cFree(h)
		cFree(0)
	})
})

// Ungated specs for the pure-Go helpers (no libdllm.so required).
var _ = Describe("buildOptsJSON", func() {
	It("renders flat scalars as a JSON object", func() {
		out, err := buildOptsJSON(map[string]any{
			"n_predict": 16,
			"seed":      int64(7),
			"eb_t_min":  0.5,
			"kv_cache":  "auto",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(MatchJSON(`{"n_predict":16,"seed":7,"eb_t_min":0.5,"kv_cache":"auto"}`))
	})

	It("renders an empty object for no options", func() {
		out, err := buildOptsJSON(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("{}"))
	})

	It("rejects nested objects (the C-side scanner only reads flat scalars)", func() {
		_, err := buildOptsJSON(map[string]any{"sampler": map[string]any{"seed": 1}})
		Expect(err).To(HaveOccurred())
	})

	It("rejects arrays", func() {
		_, err := buildOptsJSON(map[string]any{"stop": []string{"a"}})
		Expect(err).To(HaveOccurred())
	})

	It("rejects booleans (the C-side scanner only understands numbers and strings)", func() {
		_, err := buildOptsJSON(map[string]any{"flag": true})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("splitValidUTF8", func() {
	It("holds back a trailing incomplete sequence and completes it next block", func() {
		emit, carry := splitValidUTF8("", "caf\xe2")
		Expect(emit).To(Equal("caf"))
		Expect(carry).To(Equal("\xe2"))

		emit, carry = splitValidUTF8(carry, "\x82")
		Expect(emit).To(BeEmpty())
		Expect(carry).To(Equal("\xe2\x82"))

		emit, carry = splitValidUTF8(carry, "\xac!")
		Expect(emit).To(Equal("€!"))
		Expect(carry).To(BeEmpty())
	})

	It("holds back up to 3 bytes of a 4-byte sequence", func() {
		emit, carry := splitValidUTF8("", "x\xf0\x9f\x98") // 😀 missing its last byte
		Expect(emit).To(Equal("x"))
		Expect(carry).To(Equal("\xf0\x9f\x98"))

		emit, carry = splitValidUTF8(carry, "\x80")
		Expect(emit).To(Equal("😀"))
		Expect(carry).To(BeEmpty())
	})

	It("replaces undecodable bytes immediately instead of carrying them", func() {
		// A mid-string invalid byte can never complete: carrying it would let
		// the carry grow unboundedly, so it is substituted on the spot.
		emit, carry := splitValidUTF8("", "a\xe2bc")
		Expect(emit).To(Equal("a�bc"))
		Expect(carry).To(BeEmpty())

		// Orphan continuation bytes at the end have no leading byte to wait
		// for either.
		emit, carry = splitValidUTF8("", "a\x82")
		Expect(emit).To(Equal("a�"))
		Expect(carry).To(BeEmpty())
	})

	It("passes pure ASCII and complete UTF-8 through untouched", func() {
		emit, carry := splitValidUTF8("", "héllo €")
		Expect(emit).To(Equal("héllo €"))
		Expect(carry).To(BeEmpty())
	})
})

var _ = Describe("goStringFromCPtr", func() {
	It("copies a NUL-terminated buffer", func() {
		buf := []byte("dllm\x00")
		s := goStringFromCPtr(uintptr(unsafe.Pointer(&buf[0])))
		// The uintptr round-trip hides buf from the GC's liveness analysis;
		// keep it reachable until after the copy.
		runtime.KeepAlive(buf)
		Expect(s).To(Equal("dllm"))
	})

	It("returns the empty string for NULL", func() {
		Expect(goStringFromCPtr(0)).To(Equal(""))
	})
})

// ---------------------------------------------------------------------------
// Backend wiring (T4): fake-generator specs, no libdllm.so required.
// ---------------------------------------------------------------------------

type fakeGenCall struct {
	prompt   string
	optsJSON string
}

// fakeGen implements generator in-process. It records every call (prompt +
// opts JSON), tracks concurrent in-flight calls to prove worker
// serialization, and replays canned output (out for generate/tokenize,
// blocks for generateStream).
type fakeGen struct {
	mu          sync.Mutex
	calls       []fakeGenCall
	inFlight    int
	maxInFlight int

	out    string
	blocks []string
	err    error
	delay  time.Duration
}

func (f *fakeGen) begin(prompt, optsJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeGenCall{prompt: prompt, optsJSON: optsJSON})
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
}

func (f *fakeGen) end() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
}

func (f *fakeGen) snapshot() (calls []fakeGenCall, maxInFlight int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeGenCall(nil), f.calls...), f.maxInFlight
}

func (f *fakeGen) generate(prompt, optsJSON string) (string, error) {
	f.begin(prompt, optsJSON)
	defer f.end()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.out, f.err
}

func (f *fakeGen) generateStream(prompt, optsJSON string, onBlock func(text string)) error {
	f.begin(prompt, optsJSON)
	defer f.end()
	if f.err != nil {
		return f.err
	}
	for _, b := range f.blocks {
		onBlock(b)
	}
	return nil
}

func (f *fakeGen) tokenizeJSON(text string) (string, error) {
	f.begin(text, "")
	defer f.end()
	return f.out, f.err
}

func (f *fakeGen) cancel() {}
func (f *fakeGen) free()   {}

// newTestDllm assembles a backend around a fake generator (bypassing Load,
// which needs libdllm.so) and registers cleanup of the worker goroutine.
func newTestDllm(g generator, genOpts map[string]any) *Dllm {
	d := &Dllm{gen: g, genOpts: genOpts}
	d.startWorker()
	DeferCleanup(func() { Expect(d.Free()).To(Succeed()) })
	return d
}

// drainReplies empties ch without blocking, failing the spec if the channel
// was closed (PredictStreamRich must NOT close it - interface.go contract).
// Size ch above the expected reply count: an overflow deadlocks the spec on
// the producer's send instead of failing it.
func drainReplies(ch chan *pb.Reply) []*pb.Reply {
	var out []*pb.Reply
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				Fail("PredictStreamRich closed the results channel (the gRPC server owns the close)")
			}
			expectValidUTF8Reply(r)
			out = append(out, r)
		default:
			return out
		}
	}
}

// expectValidUTF8Reply is the blanket guard for the proto3 marshaling
// contract: grpc-go rejects any string field carrying invalid UTF-8, so every
// reply field that ends up in a proto string must validate.
func expectValidUTF8Reply(r *pb.Reply) {
	GinkgoHelper()
	Expect(utf8.ValidString(string(r.GetMessage()))).To(BeTrue(), "Reply.Message carries invalid UTF-8")
	for _, delta := range r.GetChatDeltas() {
		Expect(utf8.ValidString(delta.GetContent())).To(BeTrue(), "ChatDelta.Content carries invalid UTF-8")
		Expect(utf8.ValidString(delta.GetReasoningContent())).To(BeTrue(), "ChatDelta.ReasoningContent carries invalid UTF-8")
		for _, tc := range delta.GetToolCalls() {
			Expect(utf8.ValidString(tc.GetName())).To(BeTrue(), "ToolCallDelta.Name carries invalid UTF-8")
			Expect(utf8.ValidString(tc.GetArguments())).To(BeTrue(), "ToolCallDelta.Arguments carries invalid UTF-8")
		}
	}
}

var _ = Describe("Dllm backend wiring", func() {
	Describe("PredictRich", func() {
		It("renders gemma4 from raw messages and parses the output when use_tokenizer_template is set", func() {
			fake := &fakeGen{out: "<|channel>thought\npondering<channel|>The answer.<turn|>"}
			d := newTestDllm(fake, nil)

			reply, err := d.PredictRich(&pb.PredictOptions{
				UseTokenizerTemplate: true,
				Messages:             []*pb.Message{{Role: "user", Content: "Write a long essay about Portugal."}},
				Metadata:             map[string]string{"enable_thinking": "true"},
			})
			Expect(err).ToNot(HaveOccurred())

			calls, _ := fake.snapshot()
			Expect(calls).To(HaveLen(1))
			// The enable_thinking=true render from the transformers fixture.
			Expect(calls[0].prompt).To(Equal(
				"<|turn>system\n<|think|>\n<turn|>\n<|turn>user\nWrite a long essay about Portugal.<turn|>\n<|turn>model\n"))

			Expect(string(reply.GetMessage())).To(Equal("The answer."))
			Expect(reply.GetChatDeltas()).To(HaveLen(2))
			Expect(reply.GetChatDeltas()[0].GetReasoningContent()).To(Equal("pondering"))
			Expect(reply.GetChatDeltas()[1].GetContent()).To(Equal("The answer."))
		})

		It("defaults enable_thinking OFF (the gemma4 template treats thinking as opt-in)", func() {
			fake := &fakeGen{out: "hi"}
			d := newTestDllm(fake, nil)

			_, err := d.PredictRich(&pb.PredictOptions{
				UseTokenizerTemplate: true,
				Messages:             []*pb.Message{{Role: "user", Content: "Write a long essay about Portugal."}},
			})
			Expect(err).ToNot(HaveOccurred())

			calls, _ := fake.snapshot()
			// No-thinking render: the template pre-opens AND pre-closes an
			// empty thought channel in the generation prompt.
			Expect(calls[0].prompt).To(Equal(
				"<|turn>user\nWrite a long essay about Portugal.<turn|>\n<|turn>model\n<|channel>thought\n<channel|>"))
		})

		It("passes the raw prompt verbatim and skips gemma4 parsing without use_tokenizer_template", func() {
			// Marker-looking text must survive untouched: in raw-prompt mode
			// the caller templates themselves and the Go-side extraction
			// applies, so the backend must not interpret the output.
			fake := &fakeGen{out: "<|channel>thought\nnot parsed<channel|>tail"}
			d := newTestDllm(fake, nil)

			reply, err := d.PredictRich(&pb.PredictOptions{Prompt: "my raw prompt"})
			Expect(err).ToNot(HaveOccurred())

			calls, _ := fake.snapshot()
			Expect(calls[0].prompt).To(Equal("my raw prompt"))
			Expect(string(reply.GetMessage())).To(Equal(fake.out))
			Expect(reply.GetChatDeltas()).To(HaveLen(1))
			Expect(reply.GetChatDeltas()[0].GetContent()).To(Equal(fake.out))
		})

		It("sanitizes invalid UTF-8 in the non-streaming output", func() {
			// Byte-fallback tokens can decode to lone malformed bytes; the
			// whole-output sanitize must replace them so proto3 marshaling of
			// Message/ChatDeltas cannot fail.
			fake := &fakeGen{out: "a\xe2b"}
			d := newTestDllm(fake, nil)

			reply, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
			Expect(err).ToNot(HaveOccurred())
			expectValidUTF8Reply(reply)
			Expect(string(reply.GetMessage())).To(Equal("a�b"))
			Expect(reply.GetChatDeltas()[0].GetContent()).To(Equal("a�b"))
		})

		It("maps Tokens and Seed into the opts JSON on top of the model-level overrides", func() {
			fake := &fakeGen{out: "x"}
			d := newTestDllm(fake, map[string]any{"eb_t_min": 0.5, "kv_cache": "auto"})

			_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p", Tokens: 32, Seed: 7})
			Expect(err).ToNot(HaveOccurred())

			calls, _ := fake.snapshot()
			Expect(calls[0].optsJSON).To(MatchJSON(`{"n_predict":32,"seed":7,"eb_t_min":0.5,"kv_cache":"auto"}`))
		})

		It("omits n_predict and seed when unset so the engine defaults apply", func() {
			fake := &fakeGen{out: "x"}
			d := newTestDllm(fake, nil)

			_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
			Expect(err).ToNot(HaveOccurred())

			calls, _ := fake.snapshot()
			Expect(calls[0].optsJSON).To(MatchJSON(`{}`))
		})

		It("surfaces generator errors", func() {
			fake := &fakeGen{err: errors.New("boom")}
			d := newTestDllm(fake, nil)

			_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
			Expect(err).To(MatchError("boom"))
		})

		It("errors before generating when no model is loaded", func() {
			d := &Dllm{} // no Load, no worker: must fail fast, not hang
			_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
			Expect(err).To(HaveOccurred())
		})

		It("makes a concurrent Free wait for the in-flight request (both finish cleanly)", func() {
			// server.go's Free has no locking of its own: the backend's genMu
			// must hold Free back until the racing generate drains, instead of
			// closing the jobs channel (panic) or freeing the C ctx under it.
			fake := &fakeGen{out: "x", delay: 50 * time.Millisecond}
			d := newTestDllm(fake, nil)

			predictDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
				predictDone <- err
			}()
			// Wait until the fake generate is actually in flight (the read
			// lock is held from before submit until PredictRich returns).
			Eventually(func() int {
				_, maxInFlight := fake.snapshot()
				return maxInFlight
			}).Should(Equal(1))

			Expect(d.Free()).To(Succeed())
			// Free's write lock means the request finished before Free did.
			var predictErr error
			Eventually(predictDone).Should(Receive(&predictErr))
			Expect(predictErr).ToNot(HaveOccurred())
		})

		It("returns model-not-loaded for requests after Free", func() {
			fake := &fakeGen{out: "x"}
			d := newTestDllm(fake, nil)
			Expect(d.Free()).To(Succeed())

			_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
			Expect(err).To(MatchError(ContainSubstring("model not loaded")))
		})

		It("serializes concurrent requests through the worker goroutine", func() {
			// dllm_capi.h: one ctx = one concurrent generate. Two overlapping
			// PredictRich calls must execute the C calls one at a time.
			fake := &fakeGen{out: "x", delay: 30 * time.Millisecond}
			d := newTestDllm(fake, nil)

			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					_, err := d.PredictRich(&pb.PredictOptions{Prompt: "p"})
					Expect(err).ToNot(HaveOccurred())
				}()
			}
			wg.Wait()

			calls, maxInFlight := fake.snapshot()
			Expect(calls).To(HaveLen(2))
			Expect(maxInFlight).To(Equal(1), "generate calls overlapped despite the worker queue")
		})
	})

	Describe("PredictStreamRich", func() {
		It("emits one reply per delta-producing block and leaves the channel open", func() {
			// Blocks split mid-marker and mid-payload: the parser's holdback
			// must keep marker fragments out of the emitted deltas.
			fake := &fakeGen{blocks: []string{
				"<|channel>thou",        // partial channel open: no deltas yet
				"ght\nponder",           // header completes, reasoning starts
				"ing<channel|>Hi ",      // reasoning ends, content starts
				"there<turn|>discarded", // turn ends: trailing text dropped
			}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{
				UseTokenizerTemplate: true,
				Messages:             []*pb.Message{{Role: "user", Content: "hi"}},
			}, ch)
			Expect(err).ToNot(HaveOccurred())

			replies := drainReplies(ch)
			Expect(replies).To(HaveLen(3), "block 1 completes no delta and must not produce a reply")

			var content, reasoning string
			for _, r := range replies {
				for _, delta := range r.GetChatDeltas() {
					content += delta.GetContent()
					reasoning += delta.GetReasoningContent()
				}
			}
			Expect(reasoning).To(Equal("pondering"))
			Expect(content).To(Equal("Hi there"))
			// Message mirrors each reply's content so legacy consumers see
			// exactly the displayed tokens.
			Expect(string(replies[1].GetMessage())).To(Equal("Hi "))
			Expect(string(replies[2].GetMessage())).To(Equal("there"))
		})

		It("streams raw blocks verbatim without use_tokenizer_template", func() {
			fake := &fakeGen{blocks: []string{"abc", "", "<|channel>def"}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{Prompt: "raw"}, ch)
			Expect(err).ToNot(HaveOccurred())

			replies := drainReplies(ch)
			Expect(replies).To(HaveLen(2), "empty blocks produce no reply")
			Expect(string(replies[0].GetMessage())).To(Equal("abc"))
			Expect(string(replies[1].GetMessage())).To(Equal("<|channel>def"))
			Expect(replies[1].GetChatDeltas()).To(HaveLen(1))
		})

		It("flushes parser holdback after the stream ends", func() {
			// The unterminated partial marker "<chan" is held back during the
			// stream and must come out as content on the final flush.
			fake := &fakeGen{blocks: []string{"tail<chan"}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{
				UseTokenizerTemplate: true,
				Messages:             []*pb.Message{{Role: "user", Content: "hi"}},
			}, ch)
			Expect(err).ToNot(HaveOccurred())

			var content string
			for _, r := range drainReplies(ch) {
				content += string(r.GetMessage())
			}
			Expect(content).To(Equal("tail<chan"))
		})

		It("reassembles a multi-byte character split across block boundaries", func() {
			// Per-block detokenize can split "€" (E2 82 AC) as E2 | 82 AC.
			// Emitting the lone E2 would make grpc-go fail the marshal of the
			// whole reply; the trailing incomplete sequence must be held back
			// and completed by the next block.
			fake := &fakeGen{blocks: []string{"caf\xe2", "\x82\xac ok"}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{Prompt: "raw"}, ch)
			Expect(err).ToNot(HaveOccurred())

			var content string
			for _, r := range drainReplies(ch) { // drain asserts ValidString per reply
				content += string(r.GetMessage())
			}
			Expect(content).To(Equal("caf€ ok"))
		})

		It("reassembles a split multi-byte character in parsed (gemma4) mode too", func() {
			fake := &fakeGen{blocks: []string{"caf\xe2", "\x82\xac<turn|>"}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{
				UseTokenizerTemplate: true,
				Messages:             []*pb.Message{{Role: "user", Content: "hi"}},
			}, ch)
			Expect(err).ToNot(HaveOccurred())

			var content string
			for _, r := range drainReplies(ch) {
				for _, delta := range r.GetChatDeltas() {
					content += delta.GetContent()
				}
			}
			Expect(content).To(Equal("caf€"))
		})

		It("replaces an incomplete sequence left at stream end with U+FFFD", func() {
			// A byte-fallback token can leave a lone leading byte (0xE2) that
			// no later block completes: the final flush must substitute it,
			// never emit it raw and never drop into a marshal error.
			fake := &fakeGen{blocks: []string{"ok\xe2"}}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{Prompt: "raw"}, ch)
			Expect(err).ToNot(HaveOccurred())

			var content string
			for _, r := range drainReplies(ch) {
				content += string(r.GetMessage())
			}
			Expect(content).To(Equal("ok�"))
		})

		It("surfaces generator errors without sending replies", func() {
			fake := &fakeGen{err: errors.New("stream boom")}
			d := newTestDllm(fake, nil)

			ch := make(chan *pb.Reply, 16)
			err := d.PredictStreamRich(&pb.PredictOptions{Prompt: "p"}, ch)
			Expect(err).To(MatchError("stream boom"))
			Expect(drainReplies(ch)).To(BeEmpty())
		})

		It("errors before generating when no model is loaded", func() {
			d := &Dllm{} // no Load, no worker: must fail fast, not hang
			ch := make(chan *pb.Reply, 1)
			err := d.PredictStreamRich(&pb.PredictOptions{Prompt: "p"}, ch)
			Expect(err).To(MatchError(ContainSubstring("model not loaded")))
			Expect(drainReplies(ch)).To(BeEmpty())
		})
	})

	Describe("legacy Predict/PredictStream adapters", func() {
		It("Predict returns the aggregated content string", func() {
			fake := &fakeGen{out: "plain text"}
			d := newTestDllm(fake, nil)

			out, err := d.Predict(&pb.PredictOptions{Prompt: "p"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("plain text"))
		})

		It("PredictStream forwards content strings and closes the channel (legacy ownership)", func() {
			fake := &fakeGen{blocks: []string{"a", "b"}}
			d := newTestDllm(fake, nil)

			ch := make(chan string, 16)
			Expect(d.PredictStream(&pb.PredictOptions{Prompt: "p"}, ch)).To(Succeed())

			var got []string
			for s := range ch { // terminates only if the impl closed ch
				got = append(got, s)
			}
			Expect(got).To(Equal([]string{"a", "b"}))
		})
	})

	Describe("TokenizeString", func() {
		It("decodes the C-side JSON id array", func() {
			fake := &fakeGen{out: "[2,18]"}
			d := newTestDllm(fake, nil)

			resp, err := d.TokenizeString(&pb.PredictOptions{Prompt: "hello"})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Length).To(Equal(int32(2)))
			Expect(resp.Tokens).To(Equal([]int32{2, 18}))

			calls, _ := fake.snapshot()
			Expect(calls[0].prompt).To(Equal("hello"))
		})

		It("fails loud on a malformed id array", func() {
			fake := &fakeGen{out: "not json"}
			d := newTestDllm(fake, nil)

			_, err := d.TokenizeString(&pb.PredictOptions{Prompt: "hello"})
			Expect(err).To(HaveOccurred())
		})

		It("errors before tokenizing when no model is loaded", func() {
			d := &Dllm{} // no Load, no worker: must fail fast, not hang
			_, err := d.TokenizeString(&pb.PredictOptions{Prompt: "hello"})
			Expect(err).To(MatchError(ContainSubstring("model not loaded")))
		})
	})

	Describe("parseModelGenOpts", func() {
		It("parses eb_*/blocks/kv_cache entries and types values by first successful parse", func() {
			got := parseModelGenOpts([]string{
				"eb_max_steps:16",
				"eb_t_min:0.25",
				"kv_cache:auto",
				"blocks:4",
				"unrelated_key:1", // other layers' options: skipped
				"malformed",       // no colon: skipped
			})
			Expect(got).To(Equal(map[string]any{
				"eb_max_steps": int64(16),
				"eb_t_min":     0.25,
				"kv_cache":     "auto",
				"blocks":       int64(4),
			}))
		})

		It("round-trips through buildOptsJSON (only flat scalars are produced)", func() {
			got := parseModelGenOpts([]string{"eb_entropy_bound:0.8", "kv_cache:off"})
			out, err := buildOptsJSON(got)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(MatchJSON(`{"eb_entropy_bound":0.8,"kv_cache":"off"}`))
		})
	})
})

// ---------------------------------------------------------------------------
// Gated backend round-trip against the real libdllm.so + tiny GGUF fixture.
// ---------------------------------------------------------------------------

var _ = Describe("Dllm backend (real tiny model)", func() {
	BeforeEach(func() {
		if os.Getenv("DLLM_TEST_LIBRARY") == "" || os.Getenv("DLLM_TEST_TINY_MODEL") == "" {
			Skip("set DLLM_TEST_LIBRARY and DLLM_TEST_TINY_MODEL to run the backend round-trip")
		}
		ensureLibLoaded()
		Expect(libLoadErr).ToNot(HaveOccurred())
	})

	It("round-trips Load, PredictRich, PredictStreamRich and TokenizeString", func() {
		d := &Dllm{}
		Expect(d.Load(&pb.ModelOptions{ModelFile: os.Getenv("DLLM_TEST_TINY_MODEL")})).To(Succeed())
		DeferCleanup(func() { Expect(d.Free()).To(Succeed()) })

		// TokenizeString: tiny fixture vocab tokenizes "hello" to [2,18].
		resp, err := d.TokenizeString(&pb.PredictOptions{Prompt: "hello"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Tokens).To(Equal([]int32{2, 18}))
		Expect(resp.Length).To(Equal(int32(2)))

		req := &pb.PredictOptions{
			UseTokenizerTemplate: true,
			Messages:             []*pb.Message{{Role: "user", Content: "hello"}},
			Tokens:               16,
			Seed:                 7,
		}

		// Non-streaming: the tiny random-weight model emits arbitrary vocab
		// words; with no gemma4 markers in them everything is content.
		reply, err := d.PredictRich(req)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.GetMessage())).ToNot(BeEmpty())
		Expect(reply.GetChatDeltas()).ToNot(BeEmpty())

		// Streaming: at least one reply, and the channel-ownership rule is
		// honored (drainReplies fails the spec on a closed channel).
		ch := make(chan *pb.Reply, 64)
		Expect(d.PredictStreamRich(req, ch)).To(Succeed())
		replies := drainReplies(ch)
		Expect(replies).ToNot(BeEmpty())
		var streamed string
		for _, r := range replies {
			streamed += string(r.GetMessage())
		}
		Expect(streamed).ToNot(BeEmpty())
	})
})
