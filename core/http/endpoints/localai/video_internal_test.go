package localai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("video media staging", func() {
	It("stages raw base64 into a private temporary file", func() {
		directory := GinkgoT().TempDir()
		content := []byte("avatar audio")

		path, err := stageVideoMedia(context.Background(), directory, base64.StdEncoding.EncodeToString(content))

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Remove, path)
		Expect(filepath.Dir(path)).To(Equal(directory))
		Expect(os.ReadFile(path)).To(Equal(content))
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("accepts browser data URIs with codec parameters", func() {
		content := []byte("recorded speech")
		ref := "data:audio/webm;codecs=opus;base64," + base64.StdEncoding.EncodeToString(content)

		path, err := stageVideoMedia(context.Background(), GinkgoT().TempDir(), ref)

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Remove, path)
		Expect(os.ReadFile(path)).To(Equal(content))
	})

	It("rejects malformed base64 and removes the partial file", func() {
		directory := GinkgoT().TempDir()

		_, err := stageVideoMedia(context.Background(), directory, "not%%%base64")

		Expect(err).To(MatchError(ContainSubstring("decoding media")))
		entries, readErr := os.ReadDir(directory)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("enforces the configured streaming limit", func() {
		directory := GinkgoT().TempDir()
		encoded := base64.StdEncoding.EncodeToString([]byte("four"))

		_, err := stageVideoMediaWithLimit(context.Background(), directory, encoded, 3)

		Expect(err).To(MatchError(ContainSubstring("3-byte limit")))
		entries, readErr := os.ReadDir(directory)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("rejects non-base64 data URIs", func() {
		_, err := stageVideoMedia(context.Background(), GinkgoT().TempDir(), "data:audio/wav,plain")

		Expect(err).To(MatchError(ContainSubstring("base64 payload")))
	})
})

var _ = Describe("video reference staging", func() {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	It("stages images, audio and a frame directory in order", func() {
		directory := GinkgoT().TempDir()
		input := &schema.VideoRequest{
			RefImages: []string{enc("img0"), enc("img1")},
			RefAudios: []string{enc("wav0")},
			RefVideos: []schema.VideoReference{{
				Frames: []string{enc("f0"), enc("f1"), enc("f2")},
				FPS:    12,
				Audio:  enc("vwav"),
			}},
		}

		refs, err := stageVideoReferences(context.Background(), directory, input)

		Expect(err).NotTo(HaveOccurred())
		Expect(refs.images).To(HaveLen(2))
		Expect(os.ReadFile(refs.images[1])).To(Equal([]byte("img1")))
		Expect(refs.audios).To(HaveLen(1))
		Expect(refs.videos).To(HaveLen(1))
		Expect(refs.videos[0].FPS).To(Equal(int32(12)))
		Expect(os.ReadFile(refs.videos[0].Audio)).To(Equal([]byte("vwav")))
		entries, err := os.ReadDir(refs.videos[0].FramesDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(3))
		for i, want := range []string{"f0", "f1", "f2"} {
			Expect(entries[i].Name()).To(Equal(fmt.Sprintf("%06d", i)))
			Expect(os.ReadFile(filepath.Join(refs.videos[0].FramesDir, entries[i].Name()))).To(Equal([]byte(want)))
		}

		refs.cleanup()
		left, err := os.ReadDir(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(left).To(BeEmpty())
	})

	It("stages nothing when no reference is given", func() {
		refs, err := stageVideoReferences(context.Background(), GinkgoT().TempDir(), &schema.VideoRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.images).To(BeEmpty())
		Expect(refs.videos).To(BeEmpty())
		Expect(refs.audios).To(BeEmpty())
	})

	DescribeTable("rejects bad input and leaves nothing behind",
		func(input *schema.VideoRequest, want string) {
			directory := GinkgoT().TempDir()

			refs, err := stageVideoReferences(context.Background(), directory, input)

			Expect(refs).To(BeNil())
			var httpErr *echo.HTTPError
			Expect(errors.As(err, &httpErr)).To(BeTrue())
			Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
			Expect(fmt.Sprint(httpErr.Message)).To(ContainSubstring(want))
			left, readErr := os.ReadDir(directory)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(left).To(BeEmpty())
		},
		Entry("empty image", &schema.VideoRequest{RefImages: []string{enc("ok"), ""}}, "ref_images[1]"),
		Entry("bad base64 audio", &schema.VideoRequest{RefAudios: []string{"not%%%base64"}}, "ref_audios[0]"),
		Entry("video without frames", &schema.VideoRequest{RefVideos: []schema.VideoReference{{}}}, "ref_videos[0]"),
		Entry("negative fps", &schema.VideoRequest{RefVideos: []schema.VideoReference{{Frames: []string{enc("f")}, FPS: -1}}}, "fps"),
		Entry("bad frame after good ones", &schema.VideoRequest{RefVideos: []schema.VideoReference{{Frames: []string{enc("f"), "not%%%base64"}}}}, "frames[1]"),
		Entry("bad paired audio", &schema.VideoRequest{RefVideos: []schema.VideoReference{{Frames: []string{enc("f")}, Audio: "not%%%base64"}}}, "ref_videos[0].audio"),
		Entry("too many images", &schema.VideoRequest{RefImages: make([]string, maxVideoRefsPerKind+1)}, "too many"),
	)
})
