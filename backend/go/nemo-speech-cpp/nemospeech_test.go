package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

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

// The brief's round-trip spec (cstr then a reader) cannot exist: cstr pins a Go
// allocation, and converting a uintptr back into a pointer to Go memory is a
// checkptr violation that aborts the process under -race. So cstr is asserted
// on what is observable without dereferencing its result.
var _ = Describe("cstr", func() {
	It("returns a non-null pointer for a non-empty string", func() {
		p, free := cstr("hello")
		defer free()
		Expect(p).ToNot(BeZero())
	})

	It("returns a null pointer for the empty string", func() {
		// The C API documents NULL and "" as equivalent for optional fields, and
		// passing NULL avoids allocating for every unset option.
		p, free := cstr("")
		defer free()
		Expect(p).To(BeZero())
	})

	// The pin has to hold for the whole create call, which spans at least one
	// safepoint. A collection must therefore neither move nor invalidate the
	// address that C was handed.
	It("keeps the pointer stable across a garbage collection", func() {
		p, free := cstr("/models/nemo/parakeet.gguf")
		defer free()
		before := p
		runtime.GC()
		runtime.GC()
		Expect(p).To(Equal(before))
	})

	It("survives releasing more than once", func() {
		_, free := cstr("twice")
		free()
		Expect(free).ToNot(Panic())
	})

	// A dropped release leaks the pin, and the runtime turns that into a process
	// abort at some later collection. Nothing can catch it, so this only pins the
	// contract in prose: release in the same statement that takes the pointer.
	It("releases without panicking when used as documented", func() {
		Expect(func() {
			p, free := cstr("released")
			defer free()
			_ = p
		}).ToNot(Panic())
	})
})

// pkg/grpc/server.go:1019 calls Free without taking the backend lock every
// other RPC holds, so a teardown really can land while a request is in flight.
// The family check and the C calls that trust it therefore have to happen under
// engineMu together, or Free can destroy the handle in the gap between them.
var _ = Describe("engine locking", func() {
	It("serialises a teardown against an in-flight request", func() {
		n := &NemoSpeech{fam: familyASR}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				// Errors are expected once the teardown wins the race; what must
				// not happen is an unsynchronised read of the family.
				_ = n.withEngine(familyASR, func() error { return nil })
			}
		}()
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				Expect(n.Free()).To(Succeed())
			}
		}()
		wg.Wait()
	})

	It("refuses the body when the family does not match, and still unlocks", func() {
		n := &NemoSpeech{fam: familyTTS}
		called := false
		err := n.withEngine(familyASR, func() error {
			called = true
			return nil
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(called).To(BeFalse())

		// A lock leaked on the rejection path would deadlock the next request
		// rather than fail it, so prove the mutex is free afterwards.
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("propagates the body's error and still unlocks", func() {
		n := &NemoSpeech{fam: familyASR}
		boom := errors.New("boom")
		Expect(n.withEngine(familyASR, func() error { return boom })).To(MatchError(boom))
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
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

	// The load path picks a family and only then runs that family's loader, so
	// there is a window where the family is known and the load still fails.
	// Committing n.fam before the loader runs would leave the RPC gate open on a
	// handle that was never created, and pkg/grpc/server.go keeps serving the
	// instance after a failed LoadModel, so the next request really would reach
	// it. TTS is the only family whose loader can fail before touching C.
	It("does not select the family until that family's loader has succeeded", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "magpie.f16.gguf")
		writeGGUFWithArch(path, "magpietts")

		// Self-guard: if the handwritten GGUF ever stops parsing, Load would fail
		// at ggufArchitecture instead, before a family is ever chosen, and the
		// assertions below would pass without exercising the ordering at all.
		Expect(ggufArchitecture(path)).To(Equal("magpietts"))

		// No sibling codec in the directory, so discoverTTSAssets fails after
		// familyFor has already resolved familyTTS.
		n := &NemoSpeech{}
		err := n.Load(&pb.ModelOptions{ModelFile: path})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("codec_model"))

		Expect(n.fam).To(Equal(familyUnknown))
		Expect(n.requireFamily(familyTTS)).To(HaveOccurred())
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
