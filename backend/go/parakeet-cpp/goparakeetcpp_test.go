package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestParakeetCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "parakeet-cpp Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive
// the C-API bridge without spinning up the gRPC server. Skips the
// current spec when libparakeet.so isn't loadable from cwd
// ($LD_LIBRARY_PATH or a symlink in ./).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libName := os.Getenv("PARAKEET_LIBRARY")
		if libName == "" {
			libName = "libparakeet.so"
		}
		lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppAbiVersion, lib, "parakeet_capi_abi_version")
		purego.RegisterLibFunc(&CppLoad, lib, "parakeet_capi_load")
		purego.RegisterLibFunc(&CppFree, lib, "parakeet_capi_free")
		purego.RegisterLibFunc(&CppTranscribePath, lib, "parakeet_capi_transcribe_path")
		purego.RegisterLibFunc(&CppFreeString, lib, "parakeet_capi_free_string")
		purego.RegisterLibFunc(&CppLastError, lib, "parakeet_capi_last_error")
	})
	if libLoadErr != nil {
		Skip("libparakeet.so not loadable: " + libLoadErr.Error())
	}
}

// fixturesOrSkip returns the model + audio paths or skips the spec if
// either env var is unset. The smoke test never runs in default CI; it
// needs a real parakeet GGUF and a 16 kHz mono WAV on disk.
func fixturesOrSkip() (string, string) {
	modelPath := os.Getenv("PARAKEET_BACKEND_TEST_MODEL")
	audioPath := os.Getenv("PARAKEET_BACKEND_TEST_WAV")
	if modelPath == "" || audioPath == "" {
		Skip("set PARAKEET_BACKEND_TEST_MODEL and PARAKEET_BACKEND_TEST_WAV to run this spec")
	}
	return modelPath, audioPath
}

var _ = Describe("ParakeetCpp", func() {
	Context("AudioTranscription", func() {
		It("transcribes a WAV via the parakeet C-API", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			defer func() { _ = p.Free() }()

			res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst: audioPath,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(res.Text)).ToNot(BeEmpty(),
				"expected non-empty transcript for %s", audioPath)
			Expect(res.Segments).To(HaveLen(1),
				"L0 synthesises a single whole-clip segment")
			Expect(res.Segments[0].Text).To(Equal(res.Text),
				"single segment text must equal the top-level text")
		})
	})
})
