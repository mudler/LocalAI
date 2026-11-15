// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"os"
	"path"
	"path/filepath"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type capturingVideoBackend struct {
	grpc.Backend
	request *pb.GenerateVideoRequest
}

func (b *capturingVideoBackend) GenerateVideo(_ context.Context, request *pb.GenerateVideoRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.request = request
	return &pb.Result{Success: true}, nil
}

var _ = Describe("FileStagingClient video references", func() {
	var (
		backend *capturingVideoBackend
		stager  *lifecycleStager
		client  *FileStagingClient
	)
	BeforeEach(func() {
		backend = &capturingVideoBackend{}
		stager = &lifecycleStager{}
		client = NewFileStagingClient(backend, stager, "worker-1")
	})

	localPaths := func() []string {
		paths := []string{}
		for _, call := range stager.ensureCalls {
			paths = append(paths, call.localPath)
		}
		return paths
	}

	It("stages reference images and audio clips and rewrites the request", func(ctx SpecContext) {
		request := &pb.GenerateVideoRequest{
			Prompt:    "a talking llama",
			Dst:       "/data/out.mp4",
			RefImages: []string{"/data/generated/video-input-1.png", "/data/generated/video-input-2.png"},
			RefAudios: []string{"/data/generated/video-input-3.wav"},
		}
		_, err := client.GenerateVideo(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		Expect(localPaths()).To(ConsistOf(
			"/data/generated/video-input-1.png", "/data/generated/video-input-2.png", "/data/generated/video-input-3.wav"))
		Expect(backend.request.RefImages).To(HaveLen(2))
		for _, ref := range append(append([]string{}, backend.request.RefImages...), backend.request.RefAudios...) {
			Expect(ref).To(HavePrefix("/remote/ephemeral/inputs/"))
		}
		Expect(path.Base(backend.request.RefImages[1])).To(Equal("video-input-2.png"))
		Expect(path.Base(backend.request.RefAudios[0])).To(Equal("video-input-3.wav"))
		Expect(backend.request.Prompt).To(Equal("a talking llama"))
		// The caller's request is not modified.
		Expect(request.RefImages[0]).To(Equal("/data/generated/video-input-1.png"))
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("stages every frame of a reference video into one worker directory and its audio", func(ctx SpecContext) {
		root := GinkgoT().TempDir()
		framesA := filepath.Join(root, "frames-a")
		framesB := filepath.Join(root, "frames-b")
		for _, dir := range []string{framesA, framesB} {
			Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
			for _, name := range []string{"frame-000001.png", "frame-000000.png"} {
				Expect(os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o600)).To(Succeed())
			}
		}
		Expect(os.MkdirAll(filepath.Join(framesA, "sub"), 0o750)).To(Succeed())

		request := &pb.GenerateVideoRequest{RefVideos: []*pb.VideoReference{
			{FramesDir: framesA, Fps: 12, Audio: "/data/generated/video-input-a.wav"},
			{FramesDir: framesB},
		}}
		_, err := client.GenerateVideo(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		Expect(stager.ensureCalls).To(HaveLen(5))
		Expect(localPaths()).To(ContainElements(
			filepath.Join(framesA, "frame-000000.png"), filepath.Join(framesA, "frame-000001.png"),
			filepath.Join(framesB, "frame-000000.png"), filepath.Join(framesB, "frame-000001.png"),
			"/data/generated/video-input-a.wav"))

		first, second := backend.request.RefVideos[0], backend.request.RefVideos[1]
		Expect(first.FramesDir).To(HavePrefix("/remote/ephemeral/video-ref-frames-0/"))
		Expect(second.FramesDir).To(HavePrefix("/remote/ephemeral/video-ref-frames-1/"))
		Expect(first.FramesDir).NotTo(Equal(second.FramesDir))
		Expect(first.Fps).To(Equal(int32(12)))
		Expect(first.Audio).To(HavePrefix("/remote/ephemeral/inputs/"))
		Expect(second.Audio).To(BeEmpty())
		// Every frame of one video shares the directory handed to the backend.
		for _, call := range stager.ensureCalls {
			if filepath.Dir(call.localPath) == framesA {
				Expect(path.Dir("/remote/" + call.key)).To(Equal(first.FramesDir))
			}
		}
		Expect(request.RefVideos[0].FramesDir).To(Equal(framesA))
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("uploads a file listed twice once", func(ctx SpecContext) {
		request := &pb.GenerateVideoRequest{
			RefImages: []string{"/data/generated/same.png", "/data/generated/same.png"},
		}
		_, err := client.GenerateVideo(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(backend.request.RefImages[0]).To(Equal(backend.request.RefImages[1]))
	})

	It("leaves empty lists and non-file references untouched", func(ctx SpecContext) {
		request := &pb.GenerateVideoRequest{
			Prompt:    "p",
			RefImages: []string{"https://example.com/a.png", "data:image/png;base64,AAAA"},
			RefAudios: []string{},
		}
		_, err := client.GenerateVideo(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(BeEmpty())
		Expect(stager.releaseBatches).To(BeEmpty())
		Expect(backend.request.RefImages).To(Equal(request.RefImages))
		Expect(backend.request.RefVideos).To(BeEmpty())
	})

	It("fails when a frames directory is empty and releases what it staged", func(ctx SpecContext) {
		empty := GinkgoT().TempDir()
		request := &pb.GenerateVideoRequest{
			RefImages: []string{"/data/generated/a.png"},
			RefVideos: []*pb.VideoReference{{FramesDir: empty}},
		}
		_, err := client.GenerateVideo(ctx, request)
		Expect(err).To(MatchError(ContainSubstring("frames directory has no files")))
		Expect(backend.request).To(BeNil())
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
		Expect(stager.releasedKeys).To(HaveLen(1))
	})
})
