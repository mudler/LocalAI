package main

import (
	"errors"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeTranslator scripts the C API's answer and records what it was asked, so
// the layer above the ABI (pair resolution, validation, the single-element text
// array) has a test at all. No Riva-Translate GGUF is small enough to keep in
// the tree, and this pretends to translate nothing.
type fakeTranslator struct {
	texts          []string
	source, target string
	calls          int

	out []string
	err error
}

func (f *fakeTranslator) translate(texts []string, source, target string) ([]string, error) {
	f.calls++
	f.texts = texts
	f.source = source
	f.target = target
	return f.out, f.err
}

// collectStrings drains ch until it closes and hands back everything it saw.
// The host does the same, so a channel this backend forgets to close hangs the
// RPC rather than failing it.
func collectStrings(ch chan string) chan []string {
	done := make(chan []string, 1)
	go func() {
		var got []string
		for s := range ch {
			got = append(got, s)
		}
		done <- got
	}()
	return done
}

var _ = Describe("languagePair", func() {
	It("uses the configured pair and returns the prompt unchanged", func() {
		n := &NemoSpeech{opts: loadOptions{sourceLanguage: "en", targetLanguage: "de"}}
		src, tgt, text := n.languagePair("hello world")
		Expect(src).To(Equal("en"))
		Expect(tgt).To(Equal("de"))
		Expect(text).To(Equal("hello world"))
	})

	// nemo_speech_nmt_translate takes explicit languages and has no prompt path,
	// so an inline directive is the only way a caller can pick a pair per request.
	It("honours an inline pair directive and strips it from the text", func() {
		n := &NemoSpeech{opts: loadOptions{sourceLanguage: "en", targetLanguage: "de"}}
		src, tgt, text := n.languagePair("[en->fr] hello world")
		Expect(src).To(Equal("en"))
		Expect(tgt).To(Equal("fr"))
		Expect(text).To(Equal("hello world"))
	})

	// The directive has to be gone from what reaches the model: the runtime
	// wraps the text in a chat template (src/nmt/langpairs.cc build_prompt), so
	// a leftover "[en->fr]" would be translated along with the sentence.
	It("leaves no trace of the directive in the translated text", func() {
		n := &NemoSpeech{opts: loadOptions{targetLanguage: "de"}}
		_, _, text := n.languagePair("[en->fr] hello world")
		Expect(text).ToNot(ContainSubstring("["))
		Expect(text).ToNot(ContainSubstring("->"))
		Expect(text).ToNot(ContainSubstring("fr"))
		Expect(text).To(Equal("hello world"))
	})

	It("leaves an unparseable directive in the text", func() {
		n := &NemoSpeech{opts: loadOptions{sourceLanguage: "en", targetLanguage: "de"}}
		src, tgt, text := n.languagePair("[not a directive] hi")
		Expect(src).To(Equal("en"))
		Expect(tgt).To(Equal("de"))
		Expect(text).To(Equal("[not a directive] hi"))
	})

	It("trims surrounding whitespace from the text", func() {
		n := &NemoSpeech{opts: loadOptions{sourceLanguage: "en", targetLanguage: "de"}}
		_, _, text := n.languagePair("  hello  ")
		Expect(text).To(Equal("hello"))
	})

	// The model's own tags carry region subtags (src/nmt/langpairs.cc: en-zh-cn,
	// pt-br, es-us), so a directive that only accepted bare two-letter codes
	// could not name half the pairs the runtime supports.
	It("accepts a regional code on either side", func() {
		n := &NemoSpeech{}
		src, tgt, text := n.languagePair("[pt-br->en] ola")
		Expect(src).To(Equal("pt-br"))
		Expect(tgt).To(Equal("en"))
		Expect(text).To(Equal("ola"))

		src, tgt, _ = n.languagePair("[en->zh-cn] hi")
		Expect(src).To(Equal("en"))
		Expect(tgt).To(Equal("zh-cn"))
	})

	// resolve_tag accepts a ready pair tag in one field with the other empty, so
	// a directive that names only one side must keep the configured value for the
	// other rather than blanking it.
	It("keeps the configured code for a side the directive omits", func() {
		n := &NemoSpeech{opts: loadOptions{sourceLanguage: "en", targetLanguage: "de"}}
		src, tgt, text := n.languagePair("[->fr] hello")
		Expect(src).To(Equal("en"))
		Expect(tgt).To(Equal("fr"))
		Expect(text).To(Equal("hello"))

		src, tgt, _ = n.languagePair("[fr->] hello")
		Expect(src).To(Equal("fr"))
		Expect(tgt).To(Equal("de"))
	})

	// resolve_tag (src/nmt/langpairs.cc:167-172) accepts a READY pair tag in one
	// field with the other empty, and the model's own tags run to three segments
	// (en-zh-cn, en-zh-tw, en-es-us, en-pt-br). That single-field three-segment
	// form is the case a two-segment pattern cannot express: it does not merely
	// mis-split the tag, it fails to match the directive at all, so the whole
	// bracket survives into the text and is handed to the model as something to
	// translate.
	//
	// Two-segment codes like pt-br and zh-cn are NOT this case; they parse either
	// way.
	It("accepts a three-segment pair tag given in one side of the directive", func() {
		n := &NemoSpeech{opts: loadOptions{targetLanguage: "de"}}
		src, tgt, text := n.languagePair("[->en-zh-cn] hi")
		Expect(src).To(BeEmpty())
		Expect(tgt).To(Equal("en-zh-cn"))
		Expect(text).To(Equal("hi"))

		src, tgt, text = n.languagePair("[pt-br-en->] hola")
		Expect(src).To(Equal("pt-br-en"))
		Expect(tgt).To(Equal("de"))
		Expect(text).To(Equal("hola"))
	})

	It("does not treat a bracketed sentence as a directive", func() {
		n := &NemoSpeech{opts: loadOptions{targetLanguage: "de"}}
		_, _, text := n.languagePair("[see figure 1] the cat sat")
		Expect(text).To(Equal("[see figure 1] the cat sat"))
	})
})

var _ = Describe("nmtTranslatorConfig", func() {
	// Backend, Model, Generation and Pool are four adjacent same-typed pointers.
	// Transposing two of them changes neither the struct's size nor any field's
	// offset, so the layout assertions in abi_test.go cannot see it, and the
	// failure it produces is the runtime reading the backend config as the model
	// config. Distinct sentinels are the only thing that catches it.
	It("wires each pointer into its own field", func() {
		cfg := nmtTranslatorConfig(0xB, 0xD)
		Expect(cfg.Backend).To(Equal(uintptr(0xB)))
		Expect(cfg.Model).To(Equal(uintptr(0xD)))
	})

	// NULL is what nmt.h documents as "library defaults" for a subsystem config,
	// and this backend has no option to fill either of them from.
	It("leaves the generation and pool configs null", func() {
		cfg := nmtTranslatorConfig(0xB, 0xD)
		Expect(cfg.Generation).To(BeZero())
		Expect(cfg.Pool).To(BeZero())
	})

	// The runtime decides a field is present with HAS_FIELD, which tests the
	// caller's size against offsetof + sizeof (src/nmt/c_api.cpp), so a config
	// sent with Size 0 has every field ignored and the model loads from a path
	// it was never given.
	It("declares its own size", func() {
		Expect(nmtTranslatorConfig(0xB, 0xD).Size).To(Equal(unsafe.Sizeof(cNMTTranslatorConfig{})))
	})
})

var _ = Describe("nmtTexts", func() {
	It("produces one non-null pointer per text", func() {
		ptrs, release, err := nmtTexts([]string{"one", "two", "three"})
		Expect(err).ToNot(HaveOccurred())
		defer release()

		Expect(ptrs).To(HaveLen(3))
		for i, p := range ptrs {
			Expect(p).ToNot(BeZero(), "texts[%d] must not be NULL", i)
		}
		// Distinct addresses: one buffer reused for every element would make the
		// runtime translate the last text three times.
		Expect(ptrs[0]).ToNot(Equal(ptrs[1]))
		Expect(ptrs[1]).ToNot(Equal(ptrs[2]))
	})

	// cstr maps "" to NULL and src/nmt/c_api.cpp maps a NULL element back to "",
	// so a blank text would be answered with a translation of nothing instead of
	// an error.
	It("refuses an empty element", func() {
		_, release, err := nmtTexts([]string{"one", ""})
		Expect(release).ToNot(BeNil())
		release()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("refuses an empty batch", func() {
		_, release, err := nmtTexts(nil)
		Expect(release).ToNot(BeNil())
		release()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("survives releasing more than once", func() {
		_, release, err := nmtTexts([]string{"once"})
		Expect(err).ToNot(HaveOccurred())
		release()
		Expect(release).ToNot(Panic())
	})
})

var _ = Describe("translateText", func() {
	It("passes the resolved pair and the text through to the runtime", func() {
		f := &fakeTranslator{out: []string{"hallo welt"}}
		got, err := translateText(f, "en", "de", "hello world")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("hallo welt"))
		Expect(f.texts).To(Equal([]string{"hello world"}))
		Expect(f.source).To(Equal("en"))
		Expect(f.target).To(Equal("de"))
	})

	// A single-pair model is configured with target_language alone, and
	// resolve_tag accepts a ready tag in one field with the other empty.
	It("allows an empty source language", func() {
		f := &fakeTranslator{out: []string{"ciao"}}
		_, err := translateText(f, "", "en-it", "hi")
		Expect(err).ToNot(HaveOccurred())
		Expect(f.source).To(BeEmpty())
	})

	It("rejects a missing target language and names the option to set", func() {
		f := &fakeTranslator{}
		_, err := translateText(f, "en", "", "hello")
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("target_language"))
		Expect(f.calls).To(BeZero())
	})

	It("rejects an empty text without calling the runtime", func() {
		f := &fakeTranslator{}
		_, err := translateText(f, "en", "de", "")
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.calls).To(BeZero())
	})

	It("propagates a runtime failure", func() {
		boom := errors.New("boom")
		_, err := translateText(&fakeTranslator{err: boom}, "en", "de", "hello")
		Expect(err).To(MatchError(boom))
	})

	// A call that returned OK with no translations is a runtime bug, and the
	// empty string it would hand back reaches the user as a successful but blank
	// completion with nothing anywhere to say why.
	It("refuses a result that carries no translation", func() {
		_, err := translateText(&fakeTranslator{}, "en", "de", "hello")
		Expect(status.Code(err)).To(Equal(codes.Internal))
	})

	It("takes the first translation when the runtime returns several", func() {
		f := &fakeTranslator{out: []string{"first", "second"}}
		got, err := translateText(f, "en", "de", "hello")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("first"))
	})
})

var _ = Describe("Predict", func() {
	It("refuses a model loaded as another family", func() {
		n := &NemoSpeech{fam: familyASR}
		out, err := n.Predict(&pb.PredictOptions{Prompt: "hello"})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(out).To(BeEmpty())

		// A lock leaked on the rejection path deadlocks the next request rather
		// than failing it.
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("refuses an unloaded model", func() {
		n := &NemoSpeech{}
		_, err := n.Predict(&pb.PredictOptions{Prompt: "hello"})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	// The validation has to happen before anything crosses the ABI: nothing is
	// loaded here, so a guard placed after the C call would panic on a nil
	// function value instead of failing the request.
	It("rejects an empty prompt before it reaches the runtime", func() {
		n := &NemoSpeech{fam: familyNMT, opts: loadOptions{targetLanguage: "de"}}
		var err error
		Expect(func() {
			_, err = n.Predict(&pb.PredictOptions{})
		}).ToNot(Panic())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("rejects a request with no target language, before it reaches the runtime", func() {
		n := &NemoSpeech{fam: familyNMT}
		var err error
		Expect(func() {
			_, err = n.Predict(&pb.PredictOptions{Prompt: "hello"})
		}).ToNot(Panic())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("target_language"))
	})
})

var _ = Describe("PredictStream", func() {
	// pkg/grpc/server.go drains this channel from a goroutine and then blocks on
	// that goroutine finishing, so a channel left open does not fail the request,
	// it hangs the RPC and every request queued behind the backend lock.
	It("closes the channel when the family does not match", func() {
		n := &NemoSpeech{fam: familyTTS}
		ch := make(chan string)
		done := collectStrings(ch)

		err := n.PredictStream(&pb.PredictOptions{Prompt: "hello"}, ch)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(<-done).To(BeEmpty())
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("closes the channel when the request is rejected", func() {
		n := &NemoSpeech{fam: familyNMT}
		ch := make(chan string)
		done := collectStrings(ch)

		err := n.PredictStream(&pb.PredictOptions{Prompt: "hello"}, ch)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(<-done).To(BeEmpty())
	})

	It("closes the channel on an unloaded model", func() {
		n := &NemoSpeech{}
		ch := make(chan string)
		done := collectStrings(ch)

		Expect(n.PredictStream(&pb.PredictOptions{Prompt: "hi"}, ch)).ToNot(Succeed())
		Expect(<-done).To(BeEmpty())
	})

	// The C API has no token callback, so the whole translation is one chunk.
	// The seam is the only place that can be asserted without a model.
	It("emits the whole translation as a single chunk", func() {
		f := &fakeTranslator{out: []string{"hallo welt"}}
		ch := make(chan string)
		done := collectStrings(ch)

		Expect(streamTranslation(f, "en", "de", "hello world", ch)).To(Succeed())
		close(ch)
		Expect(<-done).To(Equal([]string{"hallo welt"}))
	})

	It("emits nothing when the translation fails", func() {
		f := &fakeTranslator{err: errors.New("boom")}
		ch := make(chan string)
		done := collectStrings(ch)

		Expect(streamTranslation(f, "en", "de", "hello", ch)).ToNot(Succeed())
		close(ch)
		Expect(<-done).To(BeEmpty())
	})
})

var _ = Describe("unsupportedPredictFields", func() {
	It("names nothing for a plain translation request", func() {
		Expect(unsupportedPredictFields(&pb.PredictOptions{Prompt: "hello"})).To(BeEmpty())
	})

	// The sampling knobs are deliberately absent from this list: LocalAI fills
	// them in from the model config on every request, so warning about them
	// would log on every translation and say nothing.
	It("stays quiet about sampling parameters the runtime has no field for", func() {
		Expect(unsupportedPredictFields(&pb.PredictOptions{
			Prompt:      "hello",
			Temperature: 0.7,
			TopP:        0.9,
			TopK:        40,
			Seed:        42,
			Tokens:      256,
		})).To(BeEmpty())
	})

	It("names the asks the C API cannot serve at all", func() {
		got := unsupportedPredictFields(&pb.PredictOptions{
			Prompt:         "hello",
			Grammar:        "root ::= x",
			Tools:          `[{"type":"function"}]`,
			Images:         []string{"a.png"},
			Videos:         []string{"a.mp4"},
			Audios:         []string{"a.wav"},
			NegativePrompt: "no",
			Logprobs:       3,
		})
		Expect(got).To(ConsistOf("grammar", "tools", "images", "videos", "audios",
			"negative_prompt", "logprobs"))
	})
})
