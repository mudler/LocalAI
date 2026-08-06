package main

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("requireFamily", func() {
	It("accepts the loaded family", func() {
		n := &NemoSpeech{fam: familyASR}
		Expect(n.requireFamily(familyASR)).To(Succeed())
	})

	It("rejects a mismatched family with Unimplemented and names both", func() {
		n := &NemoSpeech{fam: familyTTS}
		err := n.requireFamily(familyASR)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(err.Error()).To(ContainSubstring("tts"))
		Expect(err.Error()).To(ContainSubstring("asr"))
	})

	It("rejects an unloaded model", func() {
		n := &NemoSpeech{}
		err := n.requireFamily(familyASR)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("rejects every family when the model is unloaded", func() {
		n := &NemoSpeech{}
		for _, f := range []family{familyASR, familyDiarization, familyTTS, familyNMT} {
			Expect(n.requireFamily(f)).To(HaveOccurred(), "family %s must be gated on an unloaded model", f)
		}
	})
})

var _ = Describe("cstr", func() {
	It("round-trips a string through C memory", func() {
		p, free := cstr("hello")
		defer free()
		Expect(p).ToNot(BeZero())
		Expect(goString(p)).To(Equal("hello"))
	})

	It("returns a null pointer for the empty string", func() {
		// The C API documents NULL and "" as equivalent for optional fields, and
		// passing NULL avoids allocating for every unset option.
		p, free := cstr("")
		defer free()
		Expect(p).To(BeZero())
	})

	// The pointer leaves the Go type system as a uintptr, which the collector
	// does not trace. This asserts the bytes survive a collection for as long as
	// the release function is held, which is the whole contract of the pin.
	It("keeps the bytes readable across a garbage collection", func() {
		p, free := cstr("/models/nemo/parakeet.gguf")
		defer free()
		runtime.GC()
		runtime.GC()
		Expect(goString(p)).To(Equal("/models/nemo/parakeet.gguf"))
	})

	It("survives releasing more than once", func() {
		_, free := cstr("twice")
		free()
		Expect(free).ToNot(Panic())
	})
})

var _ = Describe("goString", func() {
	It("maps a null pointer to the empty string", func() {
		Expect(goString(0)).To(Equal(""))
	})
})

var _ = Describe("Free", func() {
	// The destroy entry points are nil function values until openLibraries has
	// bound them, so an unloaded model must not reach them. LocalAI frees every
	// backend it shuts down, including one whose Load failed.
	It("is a no-op on a model that was never loaded", func() {
		n := &NemoSpeech{}
		Expect(n.Free()).To(Succeed())
	})

	It("is idempotent", func() {
		n := &NemoSpeech{}
		Expect(n.Free()).To(Succeed())
		Expect(n.Free()).To(Succeed())
	})

	It("does not reach the runtime for a load that failed part way through", func() {
		n := &NemoSpeech{}
		path := filepath.Join(GinkgoT().TempDir(), "broken.gguf")
		Expect(os.WriteFile(path, []byte("broken"), 0o600)).To(Succeed())
		Expect(n.Load(&pb.ModelOptions{ModelFile: path})).ToNot(Succeed())
		Expect(n.Free()).To(Succeed())
	})
})

var _ = Describe("Load", func() {
	It("rejects an empty model file", func() {
		n := &NemoSpeech{}
		err := n.Load(&pb.ModelOptions{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ModelFile"))
	})

	It("reports a model file that does not exist", func() {
		n := &NemoSpeech{}
		missing := filepath.Join(GinkgoT().TempDir(), "absent.gguf")
		err := n.Load(&pb.ModelOptions{ModelFile: missing})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("absent.gguf"))
	})

	It("reports a file that is not a GGUF", func() {
		n := &NemoSpeech{}
		path := filepath.Join(GinkgoT().TempDir(), "notagguf.gguf")
		Expect(os.WriteFile(path, []byte("this is not a gguf file at all"), 0o600)).To(Succeed())
		err := n.Load(&pb.ModelOptions{ModelFile: path})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nemo-speech-cpp"))
	})

	// A failed load must not leave a family selected, or the RPC gate would wave
	// requests through to a nil handle.
	It("leaves no family selected when the load fails", func() {
		n := &NemoSpeech{}
		path := filepath.Join(GinkgoT().TempDir(), "broken.gguf")
		Expect(os.WriteFile(path, []byte("broken"), 0o600)).To(Succeed())
		Expect(n.Load(&pb.ModelOptions{ModelFile: path})).ToNot(Succeed())
		Expect(n.fam).To(Equal(familyUnknown))
		Expect(n.requireFamily(familyASR)).To(HaveOccurred())
	})

	It("closes the family gate again after a free", func() {
		n := &NemoSpeech{fam: familyASR}
		Expect(n.Free()).To(Succeed())
		Expect(n.fam).To(Equal(familyUnknown))
		Expect(n.requireFamily(familyASR)).To(HaveOccurred())
	})

	It("parses the model options before it touches the model file", func() {
		// The options are what tell a TTS load where its codec lives, so they have
		// to be in place before any family-specific loader runs.
		n := &NemoSpeech{}
		path := filepath.Join(GinkgoT().TempDir(), "broken.gguf")
		Expect(os.WriteFile(path, []byte("broken"), 0o600)).To(Succeed())
		Expect(n.Load(&pb.ModelOptions{
			ModelFile: path,
			ModelPath: "/models",
			Options:   []string{"gpu:2", "codec_model:codec.gguf"},
		})).ToNot(Succeed())
		Expect(n.opts.gpu).To(Equal(int32(2)))
		Expect(n.opts.codecModel).To(Equal("/models/codec.gguf"))
	})
})
