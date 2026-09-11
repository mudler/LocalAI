package main

import (
	"encoding/binary"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	laudio "github.com/mudler/LocalAI/pkg/audio"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// puregoCallbackTableSize is the hard ceiling purego compiles callbacks into:
// maxCB in purego/syscall_sysv.go, which panics rather than growing once it is
// full and never releases an entry. Read off the module source for v0.10.0
// rather than assumed, because the whole point of the specs below is that
// exceeding it kills the process.
const puregoCallbackTableSize = 2000

// fakeSynthesizer scripts what the TTS C API emits for one synthesis.
//
// There is no MagpieTTS GGUF in the tree, so this is the only way the logic on
// top of the ABI (validation, WAV framing, chunk ordering, channel closure)
// gets tested at all. It fakes the C contract, not the model: chunks is
// whatever nemo_speech_tts_synthesize_text would have handed the callback.
type fakeSynthesizer struct {
	rate   int32
	chunks [][]byte
	err    error

	calls     int
	gotReq    *pb.TTSRequest
	gotLang   string
	cancelled bool
}

func (f *fakeSynthesizer) sampleRate() int32 { return f.rate }

func (f *fakeSynthesizer) synthesize(req *pb.TTSRequest, defaultLanguage string, sink ttsSink) error {
	f.calls++
	f.gotReq = req
	f.gotLang = defaultLanguage
	for _, c := range f.chunks {
		if !sink(c) {
			f.cancelled = true
			break
		}
	}
	return f.err
}

var _ = Describe("resolveSpeaker", func() {
	It("passes a numeric voice through as a speaker index", func() {
		idx, name := resolveSpeaker("3")
		Expect(idx).To(Equal(int32(3)))
		Expect(name).To(BeEmpty())
	})

	It("passes a named voice through as a name with no index", func() {
		// voice_name is ignored whenever speaker >= 0 (tts.h, and
		// synthesizer.cpp only calls resolve_speaker for a negative speaker), so
		// a named voice must leave the index negative or the name is dropped.
		idx, name := resolveSpeaker("Aria")
		Expect(idx).To(Equal(int32(-1)))
		Expect(name).To(Equal("Aria"))
	})

	It("leaves both unset for an empty voice so the synthesizer default wins", func() {
		idx, name := resolveSpeaker("")
		Expect(idx).To(Equal(int32(-1)))
		Expect(name).To(BeEmpty())
	})

	// A negative number is the C API's sentinel for "use the default", not a
	// speaker. Passing it through as an index would turn a request naming an
	// invalid voice into one that quietly synthesizes in the default voice.
	// Handed on as a name instead, resolve_speaker rejects it.
	It("does not let a negative number become a speaker index", func() {
		idx, name := resolveSpeaker("-1")
		Expect(idx).To(Equal(int32(-1)))
		Expect(name).To(Equal("-1"))
	})

	It("treats a non-numeric voice that merely starts with digits as a name", func() {
		idx, name := resolveSpeaker("3-alpha")
		Expect(idx).To(Equal(int32(-1)))
		Expect(name).To(Equal("3-alpha"))
	})

	It("keeps speaker 0 addressable", func() {
		// 0 is a real speaker index, and the only sentinel here is < 0.
		idx, name := resolveSpeaker("0")
		Expect(idx).To(BeZero())
		Expect(name).To(BeEmpty())
	})
})

var _ = Describe("applySynthesisParams", func() {
	// The struct the runtime hands out: speaker/seed/steps/top_k all -1, the
	// overrides off. Written literally rather than taken from
	// TTSSynthesisOptionsDefault so the specs run without the shared libraries.
	defaults := func() cTTSSynthesisOptions {
		return cTTSSynthesisOptions{
			Size:    unsafe.Sizeof(cTTSSynthesisOptions{}),
			Speaker: -1,
			Seed:    -1,
			Steps:   -1,
			TopK:    -1,
		}
	}

	It("leaves every default alone for an absent params map", func() {
		o := defaults()
		applySynthesisParams(&o, nil)
		Expect(o).To(Equal(defaults()))
	})

	It("leaves every default alone for an empty params map", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{})
		Expect(o).To(Equal(defaults()))
	})

	It("maps the five knobs the C options struct actually has", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{
			"seed":        "42",
			"steps":       "12",
			"top_k":       "80",
			"temperature": "0.7",
			"cfg_scale":   "1.5",
		})
		Expect(o.Seed).To(Equal(int32(42)))
		Expect(o.Steps).To(Equal(int32(12)))
		Expect(o.TopK).To(Equal(int32(80)))
		Expect(o.Temperature).To(BeNumerically("~", 0.7, 1e-6))
		Expect(o.CFGScale).To(BeNumerically("~", 1.5, 1e-6))
	})

	// magpietts/runtime.cpp reads options.temperature only when
	// override_temperature is true and otherwise falls back to the
	// synthesizer's config, so a temperature written without its flag is
	// silently discarded and the request looks like it was honoured.
	It("sets the override flag with the temperature", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{"temperature": "0.4"})
		Expect(o.OverrideTemperature).To(BeTrue())
		Expect(o.OverrideCFGScale).To(BeFalse(), "cfg_scale was not asked for")
	})

	It("sets the override flag with the cfg scale", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{"cfg_scale": "2"})
		Expect(o.OverrideCFGScale).To(BeTrue())
		Expect(o.OverrideTemperature).To(BeFalse(), "temperature was not asked for")
	})

	It("keeps the defaults when a value cannot be parsed", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{
			"seed":        "many",
			"steps":       "",
			"top_k":       "8.5",
			"temperature": "warm",
			"cfg_scale":   "-",
		})
		Expect(o).To(Equal(defaults()))
	})

	// The runtime takes a request's seed only when it is >= 0 and its steps and
	// top_k only when they are > 0. Writing a parsed 0 or a negative would not
	// be ignored downstream, it would erase the sentinel that means "use the
	// synthesizer's value".
	It("refuses values that would erase a sentinel", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{
			"seed":  "-5",
			"steps": "0",
			"top_k": "0",
		})
		Expect(o.Seed).To(Equal(int32(-1)))
		Expect(o.Steps).To(Equal(int32(-1)))
		Expect(o.TopK).To(Equal(int32(-1)))
	})

	It("keeps seed 0, which is a real seed", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{"seed": "0"})
		Expect(o.Seed).To(BeZero())
	})

	// TTSRequest carries fields with no equivalent in
	// nemo_speech_tts_synthesis_options. They must not be smuggled in through a
	// param name that happens to match.
	It("ignores params the C options struct has no field for", func() {
		o := defaults()
		applySynthesisParams(&o, map[string]string{
			"top_p":              "0.9",
			"repetition_penalty": "1.1",
			"speed":              "1.2",
			"instructions":       "cheerful",
		})
		Expect(o).To(Equal(defaults()))
	})
})

// The PCM callback is the one resource in this backend with a hard, silent,
// process-wide ceiling: purego compiles each into a fixed table of 2000 entries
// and never releases one, so a callback built per request takes the whole
// backend process down with a panic after 2000 syntheses. Nothing about a
// handful of manual calls shows that.
var _ = Describe("ttsPCMCallback", func() {
	It("compiles a usable callback", func() {
		Expect(ttsPCMCallback()).ToNot(BeZero())
	})

	It("compiles exactly one callback however many times it is asked", func() {
		first := ttsPCMCallback()

		// One more than the table holds: a callback compiled per call panics
		// with "purego: the maximum number of callbacks has been reached"
		// before this loop ends, which is precisely the production failure.
		for i := 0; i <= puregoCallbackTableSize; i++ {
			Expect(ttsPCMCallback()).To(Equal(first),
				"call %d returned a different callback, so a new one was compiled", i)
		}
	})

	// A source-level assertion, deliberately, because the failure it guards
	// against is invisible from inside the process: the way a per-request
	// callback gets reintroduced is by someone calling purego.NewCallback at the
	// synthesis site instead of going through ttsPCMCallback, and no in-process
	// spec can reach that call without a MagpieTTS GGUF to synthesize with.
	// Funnelling every compile through one accessor is what the whole design
	// rests on, so the single call site is the invariant worth pinning.
	It("compiles callbacks from exactly one place in the TTS path", func() {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "tts.go", nil, 0)
		Expect(err).ToNot(HaveOccurred())

		// Counted over the syntax tree rather than by grepping the text: the
		// doc comment on ttsPCMCallback names purego.NewCallback too, and a
		// spec that cannot tell an explanation from a call would be pinning the
		// prose.
		var sites []string
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewCallback" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "purego" {
				sites = append(sites, fset.Position(call.Pos()).String())
			}
			return true
		})
		Expect(sites).To(HaveLen(1),
			"every callback must be compiled through ttsPCMCallback, which memoises it")
	})
})

var _ = Describe("the PCM sink table", func() {
	It("routes a chunk to the sink registered for that id", func() {
		var got []byte
		id, release := ttsSinks.register(func(pcm []byte) bool {
			got = pcm
			return true
		})
		defer release()

		src := []byte{1, 2, 3, 4}
		Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), uint64(len(src)), id)).To(BeTrue())
		Expect(got).To(Equal([]byte{1, 2, 3, 4}))
	})

	// The pointer addresses a std::string the runtime reuses for the next
	// chunk, so a slice over it would be rewritten under the consumer.
	It("copies the chunk out of the runtime's buffer", func() {
		var got []byte
		id, release := ttsSinks.register(func(pcm []byte) bool {
			got = pcm
			return true
		})
		defer release()

		src := []byte{9, 8, 7}
		Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), uint64(len(src)), id)).To(BeTrue())
		src[0], src[1], src[2] = 0, 0, 0
		Expect(got).To(Equal([]byte{9, 8, 7}))
	})

	It("gives each registration its own id", func() {
		idA, releaseA := ttsSinks.register(func([]byte) bool { return true })
		defer releaseA()
		idB, releaseB := ttsSinks.register(func([]byte) bool { return true })
		defer releaseB()

		Expect(idA).ToNot(Equal(idB))
		Expect(idA).ToNot(BeZero(), "id 0 is what a zeroed user_data would carry")
		Expect(idB).ToNot(BeZero())
	})

	// Two models synthesizing at once share one callback, and engineMu is
	// per-model, so nothing serialises them against each other.
	It("keeps concurrent sinks apart", func() {
		var mu sync.Mutex
		got := map[uintptr][]byte{}

		var wg sync.WaitGroup
		for i := range 16 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()

				src := []byte{byte(i)}
				var mine []byte
				id, release := ttsSinks.register(func(pcm []byte) bool {
					mine = pcm
					return true
				})
				defer release()

				Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), 1, id)).To(BeTrue())
				mu.Lock()
				defer mu.Unlock()
				got[id] = mine
			}()
		}
		wg.Wait()

		Expect(got).To(HaveLen(16))
		for id, pcm := range got {
			Expect(pcm).To(HaveLen(1), "sink %d received the wrong chunk", id)
		}
	})

	// A released id means the request has returned. Answering true would leave
	// the runtime synthesizing into nothing while the RPC that owns the lock
	// waits for it.
	It("cancels the synthesis when the sink is gone", func() {
		id, release := ttsSinks.register(func([]byte) bool { return true })
		release()

		src := []byte{1}
		Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), 1, id)).To(BeFalse())
	})

	It("cancels for a user_data that was never registered", func() {
		src := []byte{1}
		Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), 1, 0)).To(BeFalse())
	})

	It("accepts an empty chunk without touching the pointer", func() {
		id, release := ttsSinks.register(func([]byte) bool {
			Fail("an empty chunk must not reach the sink")
			return true
		})
		defer release()

		Expect(ttsDeliverPCM(nil, 0, id)).To(BeTrue())
	})

	It("passes the sink's cancellation back to the runtime", func() {
		id, release := ttsSinks.register(func([]byte) bool { return false })
		defer release()

		src := []byte{1}
		Expect(ttsDeliverPCM(unsafe.Pointer(&src[0]), 1, id)).To(BeFalse())
	})
})

var _ = Describe("ttsModelConfig", func() {
	// Three adjacent same-typed path fields: swapping two changes neither the
	// struct size nor any offset, so abi_test.go's layout assertions cannot see
	// it and the runtime would load the codec as the acoustic model.
	It("assigns each path to its own field", func() {
		cfg := ttsModelConfig(1, 2, 3, 4)
		Expect(cfg.MagpieModel).To(Equal(uintptr(1)))
		Expect(cfg.CodecModel).To(Equal(uintptr(2)))
		Expect(cfg.TokenizerModelDir).To(Equal(uintptr(3)))
		Expect(cfg.TextNormalizerModelDir).To(Equal(uintptr(4)))
	})

	// A config sent with the wrong size has every field past it ignored by
	// HAS_FIELD, and the model loads with defaults instead of failing.
	It("declares the size the runtime validates against", func() {
		Expect(ttsModelConfig(1, 2, 3, 4).Size).To(Equal(unsafe.Sizeof(cTTSModelConfig{})))
	})

	It("leaves an unset text normalizer null", func() {
		Expect(ttsModelConfig(1, 2, 3, 0).TextNormalizerModelDir).To(BeZero())
	})
})

var _ = Describe("ttsRuntimeBackend", func() {
	// -1 is this backend's documented "CPU" everywhere (asr.h: "-1 = CPU") and
	// it is also the default, so it has to pin the preference rather than leave
	// the runtime free to pick CUDA.
	It("pins CPU for a negative gpu option", func() {
		Expect(ttsRuntimeBackend(-1)).To(Equal(ttsBackendCPU))
	})

	// nemo_speech_tts_runtime_config has no device index at all, so a request
	// for a particular device cannot be honoured and AUTO is the honest answer.
	It("leaves the choice to the runtime when a device was named", func() {
		Expect(ttsRuntimeBackend(0)).To(Equal(ttsBackendAuto))
		Expect(ttsRuntimeBackend(3)).To(Equal(ttsBackendAuto))
	})
})

var _ = Describe("WAV framing", func() {
	// 16-bit mono little-endian, the format the runtime's callback delivers.
	pcm := []byte{0x01, 0x00, 0xff, 0x7f, 0x00, 0x80}

	It("writes a header the audio helpers can read back", func() {
		out, err := wavFile(pcm, 22050)
		Expect(err).ToNot(HaveOccurred())

		body, rate := laudio.ParseWAV(out)
		Expect(rate).To(Equal(22050))
		Expect(body).To(Equal(pcm))
	})

	It("describes the payload it actually carries", func() {
		out, err := wavFile(pcm, 22050)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(laudio.WAVHeaderSize + len(pcm)))

		Expect(string(out[0:4])).To(Equal("RIFF"))
		Expect(string(out[8:12])).To(Equal("WAVE"))
		Expect(binary.LittleEndian.Uint32(out[4:8])).To(Equal(uint32(36 + len(pcm))))
		Expect(binary.LittleEndian.Uint32(out[40:44])).To(Equal(uint32(len(pcm))))
		Expect(binary.LittleEndian.Uint16(out[22:24])).To(Equal(uint16(1)), "mono")
		Expect(binary.LittleEndian.Uint16(out[34:36])).To(Equal(uint16(16)), "16-bit")
		Expect(binary.LittleEndian.Uint32(out[24:28])).To(Equal(uint32(22050)))
		// byte rate = sample rate * block align, and a wrong one plays back at
		// the wrong speed in players that trust it.
		Expect(binary.LittleEndian.Uint32(out[28:32])).To(Equal(uint32(22050 * 2)))
	})

	It("carries whatever rate the synthesizer reported", func() {
		out, err := wavFile(pcm, 44100)
		Expect(err).ToNot(HaveOccurred())
		_, rate := laudio.ParseWAV(out)
		Expect(rate).To(Equal(44100))
	})

	Describe("the streaming header", func() {
		It("is a complete header on its own", func() {
			h := streamingWAVHeader(22050)
			Expect(h).To(HaveLen(laudio.WAVHeaderSize))
			Expect(string(h[0:4])).To(Equal("RIFF"))
			Expect(string(h[8:12])).To(Equal("WAVE"))
			Expect(binary.LittleEndian.Uint32(h[24:28])).To(Equal(uint32(22050)))
		})

		// NewWAVHeaderWithRate derives ChunkSize from the payload length, so
		// leaving it alone would write 36 + 0xFFFFFFFF, which wraps to 35: a
		// RIFF size smaller than the header itself.
		It("leaves both sizes unknown rather than wrapping", func() {
			h := streamingWAVHeader(22050)
			Expect(binary.LittleEndian.Uint32(h[4:8])).To(Equal(uint32(0xFFFFFFFF)))
			Expect(binary.LittleEndian.Uint32(h[40:44])).To(Equal(uint32(0xFFFFFFFF)))
		})
	})
})

var _ = Describe("synthesizeWAV", func() {
	var dst string

	BeforeEach(func() {
		dst = filepath.Join(GinkgoT().TempDir(), "out.wav")
	})

	It("writes one WAV holding every chunk in order", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}, {2, 0}, {3, 0}}}
		Expect(synthesizeWAV(s, &pb.TTSRequest{Text: "hello", Dst: dst}, "")).To(Succeed())

		out, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		body, rate := laudio.ParseWAV(out)
		Expect(rate).To(Equal(22050))
		Expect(body).To(Equal([]byte{1, 0, 2, 0, 3, 0}))
	})

	It("hands the request and the model default language to the runtime", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}}
		req := &pb.TTSRequest{Text: "hello", Dst: dst, Voice: "Aria"}
		Expect(synthesizeWAV(s, req, "it-IT")).To(Succeed())
		Expect(s.gotReq).To(Equal(req))
		Expect(s.gotLang).To(Equal("it-IT"))
	})

	It("rejects an empty text before it reaches the runtime", func() {
		s := &fakeSynthesizer{rate: 22050}
		err := synthesizeWAV(s, &pb.TTSRequest{Dst: dst}, "")
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(s.calls).To(BeZero())
	})

	// instructions has no equivalent in nemo_speech_tts_synthesis_options, which
	// conditions on a speaker rather than a prose style. Dropping it must not
	// fail the request: the caller still wants the audio it can have.
	It("synthesizes anyway for a request carrying instructions it cannot honour", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}}
		instructions := "speak cheerfully"
		Expect(synthesizeWAV(s, &pb.TTSRequest{
			Text:         "hello",
			Dst:          dst,
			Instructions: &instructions,
		}, "")).To(Succeed())
		Expect(dst).To(BeAnExistingFile())
	})

	It("rejects a request with no destination", func() {
		s := &fakeSynthesizer{rate: 22050}
		err := synthesizeWAV(s, &pb.TTSRequest{Text: "hello"}, "")
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(s.calls).To(BeZero())
	})

	// A zero rate is what a null handle reports. The file it would produce is
	// undecodable, and the synthesis that produced it would be wasted.
	It("refuses to write a file at an unusable sample rate", func() {
		s := &fakeSynthesizer{rate: 0, chunks: [][]byte{{1, 0}}}
		err := synthesizeWAV(s, &pb.TTSRequest{Text: "hello", Dst: dst}, "")
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(s.calls).To(BeZero())
		Expect(dst).ToNot(BeAnExistingFile())
	})

	It("propagates a synthesis failure and writes nothing", func() {
		boom := errors.New("boom")
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}, err: boom}
		Expect(synthesizeWAV(s, &pb.TTSRequest{Text: "hello", Dst: dst}, "")).To(MatchError(boom))
		Expect(dst).ToNot(BeAnExistingFile())
	})

	// An empty WAV is a valid file, so this would otherwise reach the user as
	// silence with no error anywhere.
	It("fails rather than write a silent file when nothing was produced", func() {
		s := &fakeSynthesizer{rate: 22050}
		err := synthesizeWAV(s, &pb.TTSRequest{Text: "hello", Dst: dst}, "")
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(dst).ToNot(BeAnExistingFile())
	})

	It("reports a destination it cannot write", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}}
		bad := filepath.Join(GinkgoT().TempDir(), "no-such-dir", "out.wav")
		err := synthesizeWAV(s, &pb.TTSRequest{Text: "hello", Dst: bad}, "")
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(err.Error()).To(ContainSubstring("out.wav"))
	})
})

var _ = Describe("streamWAV", func() {
	// drain collects everything streamWAV emits. The channel is buffered
	// because streamWAV sends inline, so an unbuffered one would deadlock the
	// spec rather than fail it.
	drain := func(s synthesizer, req *pb.TTSRequest) ([][]byte, error) {
		out := make(chan []byte, 16)
		err := streamWAV(s, req, "", out)
		close(out)

		var got [][]byte
		for c := range out {
			got = append(got, c)
		}
		return got, err
	}

	It("emits the header first, then each chunk as it arrives", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}, {2, 0}}}
		got, err := drain(s, &pb.TTSRequest{Text: "hello"})
		Expect(err).ToNot(HaveOccurred())

		Expect(got).To(HaveLen(3))
		Expect(got[0]).To(Equal(streamingWAVHeader(22050)))
		Expect(got[1]).To(Equal([]byte{1, 0}))
		Expect(got[2]).To(Equal([]byte{2, 0}))
	})

	// pkg/grpc/server.go only ever sets Reply.Audio, so core/backend's own
	// header branch (keyed on Reply.Message) never runs and a backend that
	// emitted bare PCM would stream something no client could decode.
	It("owns the header rather than leaving it to the caller", func() {
		s := &fakeSynthesizer{rate: 44100, chunks: [][]byte{{1, 0}}}
		got, err := drain(s, &pb.TTSRequest{Text: "hello"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got[0][0:4])).To(Equal("RIFF"))
		Expect(binary.LittleEndian.Uint32(got[0][24:28])).To(Equal(uint32(44100)))
	})

	It("rejects an empty text before emitting anything", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}}
		got, err := drain(s, &pb.TTSRequest{})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(got).To(BeEmpty())
		Expect(s.calls).To(BeZero())
	})

	It("emits no header at an unusable sample rate", func() {
		s := &fakeSynthesizer{rate: 0, chunks: [][]byte{{1, 0}}}
		got, err := drain(s, &pb.TTSRequest{Text: "hello"})
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(got).To(BeEmpty())
	})

	It("propagates a synthesis failure after the chunks it did emit", func() {
		boom := errors.New("boom")
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}, err: boom}
		got, err := drain(s, &pb.TTSRequest{Text: "hello"})
		Expect(err).To(MatchError(boom))
		Expect(got).To(HaveLen(2))
	})

	// streamWAV must not close the channel: TTSStream owns it, and closing in
	// one of two places depending on how far the request got is how a stream
	// ends up double-closed.
	It("leaves the channel open for its caller to close", func() {
		s := &fakeSynthesizer{rate: 22050, chunks: [][]byte{{1, 0}}}
		out := make(chan []byte, 4)
		Expect(streamWAV(s, &pb.TTSRequest{Text: "hello"}, "", out)).To(Succeed())
		Expect(func() { close(out) }).ToNot(Panic())
	})
})

var _ = Describe("the TTS RPCs", func() {
	It("refuses TTS on a model loaded as another family", func() {
		n := &NemoSpeech{fam: familyASR}
		err := n.TTS(&pb.TTSRequest{Text: "hello", Dst: "/tmp/out.wav"})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("refuses TTS on an unloaded model", func() {
		n := &NemoSpeech{}
		Expect(status.Code(n.TTS(&pb.TTSRequest{Text: "hello", Dst: "/tmp/out.wav"}))).
			To(Equal(codes.Unimplemented))
	})

	It("releases the engine lock after a refusal", func() {
		n := &NemoSpeech{fam: familyASR}
		Expect(n.TTS(&pb.TTSRequest{Text: "hello", Dst: "/tmp/out.wav"})).ToNot(Succeed())
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	// pkg/grpc/server.go drains this channel from a goroutine and then blocks
	// on that goroutine finishing, so a channel left open does not fail the
	// request, it hangs the RPC with the backend lock still held. Every exit
	// path has to close it.
	Describe("TTSStream channel closure", func() {
		// streamed runs TTSStream the way the server does and returns once the
		// channel has been closed, so a spec that hangs is a real hang.
		streamed := func(n *NemoSpeech, req *pb.TTSRequest) ([][]byte, error) {
			ch := make(chan []byte, 16)
			done := make(chan [][]byte, 1)
			go func() {
				defer GinkgoRecover()
				var got [][]byte
				for c := range ch {
					got = append(got, c)
				}
				done <- got
			}()

			err := n.TTSStream(req, ch)
			var got [][]byte
			Eventually(done).Should(Receive(&got))
			return got, err
		}

		It("closes the channel when the family does not match", func() {
			n := &NemoSpeech{fam: familyASR}
			got, err := streamed(n, &pb.TTSRequest{Text: "hello"})
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(got).To(BeEmpty())
		})

		It("closes the channel when the model was never loaded", func() {
			n := &NemoSpeech{}
			_, err := streamed(n, &pb.TTSRequest{Text: "hello"})
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		})

		// familyTTS with a zero handle: validation has to reject this before
		// anything reaches the C entry points, which are nil function values
		// until openLibraries has bound them.
		It("closes the channel when the request is rejected", func() {
			n := &NemoSpeech{fam: familyTTS}
			got, err := streamed(n, &pb.TTSRequest{})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(got).To(BeEmpty())
		})

		It("releases the engine lock afterwards", func() {
			n := &NemoSpeech{fam: familyTTS}
			_, err := streamed(n, &pb.TTSRequest{})
			Expect(err).To(HaveOccurred())
			Expect(n.engineMu.TryLock()).To(BeTrue())
			n.engineMu.Unlock()
		})
	})

	// The same guard on the offline path: a rejected request must not reach a
	// nil C function through a zero handle.
	It("rejects an invalid TTS request without touching the runtime", func() {
		n := &NemoSpeech{fam: familyTTS}
		Expect(status.Code(n.TTS(&pb.TTSRequest{Dst: "/tmp/out.wav"}))).To(Equal(codes.InvalidArgument))
		Expect(status.Code(n.TTS(&pb.TTSRequest{Text: "hello"}))).To(Equal(codes.InvalidArgument))
	})
})
