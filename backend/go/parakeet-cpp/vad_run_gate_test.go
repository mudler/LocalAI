package main

import (
	"encoding/json"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The vad_run_gate specs run against stubbed C entry points, like the other VAD
// specs, so they need no libparakeet.so.

var _ = Describe("vad_run_gate", func() {
	opts := func(o ...string) *pb.ModelOptions { return &pb.ModelOptions{Options: o} }

	Describe("parsing", func() {
		It("is off when unset and when 0", func() {
			v, err := parseVADRunGate(opts())
			Expect(err).ToNot(HaveOccurred())
			Expect(v).To(BeZero())
			v, err = parseVADRunGate(opts("vad_run_gate:0"))
			Expect(err).ToNot(HaveOccurred())
			Expect(v).To(BeZero())
		})

		It("accepts a number in [0, 1)", func() {
			for raw, want := range map[string]float64{"0.92": 0.92, "0.5": 0.5, "0.999": 0.999} {
				v, err := parseVADRunGate(opts("vad_run_gate:" + raw))
				Expect(err).ToNot(HaveOccurred())
				Expect(v).To(Equal(want))
			}
		})

		DescribeTable("rejects a bad value at load, naming the option",
			func(raw, msg string) {
				_, err := parseVADRunGate(opts("vad_run_gate:" + raw))
				Expect(err).To(MatchError(ContainSubstring("vad_run_gate")))
				Expect(err).To(MatchError(ContainSubstring(msg)))
			},
			Entry("one", "1", "out of range"),
			Entry("above one", "1.5", "out of range"),
			Entry("negative", "-0.1", "out of range"),
			Entry("not a number", "high", "is not a number"),
			Entry("NaN", "NaN", "is not a number"),
			Entry("Inf", "Inf", "is not a number"),
		)
	})

	Describe("Load and the calls that take it", func() {
		var (
			restore                         func()
			saved                           [6]any
			pool                            *diarizeCstrPool
			vadOpts, withOpts, plainOpts    string
			calledVad, calledWith, calledPl bool
			vadErr                          string
		)
		BeforeEach(func() {
			saved = [6]any{CppTranscribePathJSONVad, CppTranscribePathJSONVadWith, CppVadPcmJSON, CppTranscribePathJSONWith, CppFreeString, CppLastError}
			pool = &diarizeCstrPool{}
			CppFreeString = func(uintptr) {}
			CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr {
				Fail("the head-only entry point carries no options")
				return 0
			}
			calledVad, calledWith, calledPl = false, false, false
			vadOpts, withOpts, plainOpts, vadErr = "", "", "", ""
			doc := `{"text":"hi.","frame_sec":0.08,"words":[],"tokens":[]}`
			CppVadPcmJSON = func(_ uintptr, _ []float32, _, _ int32, o string) uintptr {
				calledVad, vadOpts = true, o
				if vadErr != "" {
					return 0
				}
				return pool.cstr(`{"segments":[]}`)
			}
			CppLastError = func(uintptr) string { return vadErr }
			CppTranscribePathJSONVadWith = func(_, _ uintptr, _ string, _ int32, o string) uintptr {
				calledWith, withOpts = true, o
				return pool.cstr(doc)
			}
			CppTranscribePathJSONWith = func(_ uintptr, _ string, _ int32, o string) uintptr {
				calledPl, plainOpts = true, o
				return pool.cstr(doc)
			}
		})
		AfterEach(func() {
			if restore != nil {
				restore()
				restore = nil
			}
			CppTranscribePathJSONVad = saved[0].(func(uintptr, string, int32) uintptr)
			CppTranscribePathJSONVadWith, CppVadPcmJSON = saved[1].(func(ctx, vadCtx uintptr, p string, d int32, o string) uintptr), saved[2].(func(uintptr, []float32, int32, int32, string) uintptr)
			CppTranscribePathJSONWith, CppFreeString, CppLastError = saved[3].(func(uintptr, string, int32, string) uintptr), saved[4].(func(uintptr)), saved[5].(func(uintptr) string)
		})
		load := func(o ...string) (*ParakeetCpp, error) {
			f := newFakeLib().withModel("asr.gguf", modelKindASR)
			restore = f.install()
			p := &ParakeetCpp{}
			return p, p.Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: o})
		}
		keys := func(s string) map[string]any {
			m := map[string]any{}
			Expect(json.Unmarshal([]byte(s), &m)).To(Succeed())
			return m
		}

		It("does not send the key when the option is unset or 0", func() {
			p, err := load("vad:true", "vad_threshold:0.5", "vad_run_gate:0")
			Expect(err).ToNot(HaveOccurred())
			Expect(p.vadOptions).To(MatchJSON(`{"threshold":0.5}`))
			p, err = load("vad:true")
			Expect(err).ToNot(HaveOccurred())
			Expect(p.vadOptions).To(BeEmpty())
		})

		It("fails the load for a bad value", func() {
			for _, o := range []string{"vad_run_gate:1", "vad_run_gate:-0.1", "vad_run_gate:x"} {
				_, err := load(o)
				Expect(err).To(MatchError(ContainSubstring("option vad_run_gate")))
			}
		})

		It("sends run_gate with the other VAD keys on the VAD rpc", func() {
			p, err := load("vad_run_gate:0.92", "vad_threshold:0.6")
			Expect(err).ToNot(HaveOccurred())
			_, err = p.VAD(&pb.VADRequest{Audio: []float32{0}})
			Expect(err).ToNot(HaveOccurred())
			Expect(calledVad).To(BeTrue())
			Expect(keys(vadOpts)).To(Equal(map[string]any{"run_gate": 0.92, "threshold": 0.6}))
		})

		It("sends run_gate on the vad:true transcription path", func() {
			p, err := load("vad:true", "vad_run_gate:0.92")
			Expect(err).ToNot(HaveOccurred())
			_, err = p.transcribePathDoc("/x/long.wav")
			Expect(err).ToNot(HaveOccurred())
			Expect(calledWith).To(BeTrue())
			Expect(keys(withOpts)).To(Equal(map[string]any{"run_gate": 0.92}))
		})

		It("keeps it out of the word filter call that rejects it", func() {
			p, err := load("vad_run_gate:0.92", "guard_min_local_conf:0.5")
			Expect(err).ToNot(HaveOccurred())
			_, err = p.transcribePathDoc("/x/a.wav")
			Expect(err).ToNot(HaveOccurred())
			Expect(calledPl).To(BeTrue())
			Expect(calledWith).To(BeFalse())
			Expect(plainOpts).To(MatchJSON(`{"min_local_conf":0.5}`))
		})

		It("sends it next to the guard keys when vad:true is on", func() {
			p, err := load("vad:true", "vad_run_gate:0.92", "guard_min_local_conf:0.5")
			Expect(err).ToNot(HaveOccurred())
			_, err = p.transcribePathDoc("/x/long.wav")
			Expect(err).ToNot(HaveOccurred())
			Expect(keys(withOpts)).To(Equal(map[string]any{"run_gate": 0.92, "min_local_conf": 0.5}))
		})

		It("refuses the gate with vad:true on a library without the _with entry point", func() {
			CppTranscribePathJSONVadWith = nil
			_, err := load("vad:true", "vad_run_gate:0.92")
			Expect(err).To(MatchError(ContainSubstring("vad_run_gate")))
			Expect(err).To(MatchError(ContainSubstring("parakeet_capi_transcribe_path_json_vad_with")))
		})

		It("names the option when a library without run_gate rejects the key", func() {
			p, err := load("vad_run_gate:0.92")
			Expect(err).ToNot(HaveOccurred())
			vadErr = `unknown option "run_gate"`
			_, err = p.VAD(&pb.VADRequest{Audio: []float32{0}})
			Expect(err).To(MatchError(ContainSubstring(`unknown option "run_gate"`)))
			Expect(err).To(MatchError(ContainSubstring("vad_run_gate option needs a libparakeet.so with run_gate support")))
		})

		It("leaves other library errors alone", func() {
			p, err := load("vad_run_gate:0.92")
			Expect(err).ToNot(HaveOccurred())
			vadErr = "model has no VAD head"
			_, err = p.VAD(&pb.VADRequest{Audio: []float32{0}})
			Expect(err).To(MatchError("parakeet-cpp: vad failed: model has no VAD head"))
		})

		It("loads with the gate on without touching a VAD stream: the backend uses none", func() {
			p, err := load("vad_run_gate:0.92")
			Expect(err).ToNot(HaveOccurred())
			Expect(p.vadRunGate).To(Equal(0.92))
		})
	})
})
