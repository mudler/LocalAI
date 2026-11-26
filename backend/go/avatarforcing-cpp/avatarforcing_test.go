package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	libOnce sync.Once
	libErr  error
)

// ensureLibrary binds the C API as main.go does, or skips the spec when
// libavatarforcing.so cannot be loaded.
func ensureLibrary() {
	libOnce.Do(func() {
		name := os.Getenv("AVATARFORCING_LIBRARY")
		if name == "" {
			name = "libavatarforcing.so"
		}
		libErr = loadLibrary(name)
	})
	if libErr != nil {
		Skip("libavatarforcing.so not loadable: " + libErr.Error())
	}
}

// End-to-end: a real GGUF, portrait and speech clip give a playable mp4. Set
// AVATARFORCING_TEST_GGUF, AVATARFORCING_TEST_PORTRAIT and
// AVATARFORCING_TEST_AUDIO to run it. A short clip (2 to 3 s) keeps the CPU
// run to a few minutes.
var _ = Describe("end to end", Label("e2e"), func() {
	var gguf, portrait, audio string

	BeforeEach(func() {
		gguf = os.Getenv("AVATARFORCING_TEST_GGUF")
		portrait = os.Getenv("AVATARFORCING_TEST_PORTRAIT")
		audio = os.Getenv("AVATARFORCING_TEST_AUDIO")
		if gguf == "" || portrait == "" || audio == "" {
			Skip("set AVATARFORCING_TEST_GGUF, AVATARFORCING_TEST_PORTRAIT and AVATARFORCING_TEST_AUDIO to run this spec")
		}
		ensureLibrary()
	})

	It("rejects a file that is not a model", func() {
		bad := filepath.Join(GinkgoT().TempDir(), "bad.gguf")
		Expect(os.WriteFile(bad, []byte("not a gguf"), 0o600)).To(Succeed())
		a := &AvatarForcing{}
		Expect(a.Load(&pb.ModelOptions{ModelFile: bad, Options: []string{"backend:cpu"}})).
			To(MatchError(ContainSubstring("avatarforcing-cpp: load")))
	})

	It("writes an mp4 with video and audio", func() {
		a := &AvatarForcing{}
		Expect(a.Load(&pb.ModelOptions{ModelFile: gguf})).To(Succeed())
		defer func() { Expect(a.Free()).To(Succeed()) }()
		Expect(a.info.Width).To(Equal(512))
		Expect(a.info.Fps).To(Equal(25))

		dst := filepath.Join(GinkgoT().TempDir(), "out.mp4")
		Expect(a.GenerateVideo(&pb.GenerateVideoRequest{
			StartImage: portrait,
			Audio:      audio,
			Dst:        dst,
			Seed:       42,
		})).To(Succeed())

		ffprobe, err := exec.LookPath("ffprobe")
		if err != nil {
			st, err := os.Stat(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Size()).To(BeNumerically(">", 0))
			return
		}
		out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type,width,height,r_frame_rate",
			"-of", "csv=p=0", dst).CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), string(out))
		s := string(out)
		Expect(s).To(ContainSubstring("video,512,512,25/1"))
		Expect(strings.Contains(s, "audio")).To(BeTrue(), s)
	})
})
