package nodes

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const fullUUIDPattern = `[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`

type lifecycleStager struct {
	fakeFileStager
	ensureErr          error
	ensureErrAt        int
	releaseErr         error
	releasedKeys       []string
	releaseBatches     [][]string
	releaseCtxErr      []error
	releaseHasDeadline []bool
	releaseDeadlines   []time.Time
}

func (s *lifecycleStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	s.fakeFileStager.EnsureRemote(ctx, nodeID, localPath, key)
	if s.ensureErr != nil && (s.ensureErrAt == 0 || len(s.ensureCalls) == s.ensureErrAt) {
		return "", s.ensureErr
	}
	return "/remote/" + key, nil
}

func (s *lifecycleStager) ReleaseRemote(ctx context.Context, _ string, key string) error {
	s.releasedKeys = append(s.releasedKeys, key)
	s.releaseCtxErr = append(s.releaseCtxErr, ctx.Err())
	deadline, ok := ctx.Deadline()
	s.releaseHasDeadline = append(s.releaseHasDeadline, ok)
	s.releaseDeadlines = append(s.releaseDeadlines, deadline)
	return s.releaseErr
}

func (s *lifecycleStager) ReleaseRemoteRequest(ctx context.Context, _, _ string, keys []string) error {
	s.releaseBatches = append(s.releaseBatches, append([]string(nil), keys...))
	s.releasedKeys = append(s.releasedKeys, keys...)
	s.releaseCtxErr = append(s.releaseCtxErr, ctx.Err())
	deadline, ok := ctx.Deadline()
	s.releaseHasDeadline = append(s.releaseHasDeadline, ok)
	s.releaseDeadlines = append(s.releaseDeadlines, deadline)
	return s.releaseErr
}

type lifecycleBackend struct {
	grpc.Backend
	predictResult *pb.Reply
	predictErr    error
	predictCalls  int
	predictInput  *pb.PredictOptions
	streamCalls   int
	streamBlock   <-chan struct{}
	streamStarted chan<- struct{}
}

func (b *lifecycleBackend) Predict(_ context.Context, in *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.Reply, error) {
	b.predictCalls++
	b.predictInput = proto.Clone(in).(*pb.PredictOptions)
	if b.predictResult == nil {
		b.predictResult = &pb.Reply{}
	}
	return b.predictResult, b.predictErr
}

func (b *lifecycleBackend) PredictStream(_ context.Context, _ *pb.PredictOptions, _ func(*pb.Reply), _ ...ggrpc.CallOption) error {
	b.streamCalls++
	if b.streamStarted != nil {
		b.streamStarted <- struct{}{}
	}
	if b.streamBlock != nil {
		<-b.streamBlock
	}
	return nil
}

func (b *lifecycleBackend) GenerateImage(_ context.Context, _ *pb.GenerateImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (b *lifecycleBackend) GenerateVideo(_ context.Context, _ *pb.GenerateVideoRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (b *lifecycleBackend) Generate3D(_ context.Context, _ *pb.Generate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (b *lifecycleBackend) TTS(_ context.Context, _ *pb.TTSRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (b *lifecycleBackend) TTSStream(_ context.Context, _ *pb.TTSRequest, _ func(*pb.Reply), _ ...ggrpc.CallOption) error {
	return nil
}

func (b *lifecycleBackend) SoundGeneration(_ context.Context, _ *pb.SoundGenerationRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

func (b *lifecycleBackend) SoundDetection(_ context.Context, _ *pb.SoundDetectionRequest, _ ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	return &pb.SoundDetectionResponse{}, nil
}

func (b *lifecycleBackend) AudioTranscription(_ context.Context, _ *pb.TranscriptRequest, _ ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	return &pb.TranscriptResult{}, nil
}

func (b *lifecycleBackend) AudioTranscriptionStream(_ context.Context, _ *pb.TranscriptRequest, _ func(*pb.TranscriptStreamResponse), _ ...ggrpc.CallOption) error {
	return nil
}

var _ = Describe("FileStagingClient request lifecycle", func() {
	It("uses a full UUID for ephemeral request keys", func() {
		Expect(requestID()).To(MatchRegexp(`^` + fullUUIDPattern + `$`))
	})

	It("releases every staged key and preserves caller requests", func(ctx SpecContext) {
		tests := []struct {
			name     string
			keyCount int
			invoke   func(*FileStagingClient) proto.Message
		}{
			{name: "predict", keyCount: 3, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.PredictOptions{Images: []string{"/tmp/image.png"}, Videos: []string{"/tmp/video.mp4"}, Audios: []string{"/tmp/audio.wav"}}
				original := proto.Clone(request)
				_, err := client.Predict(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "predict stream", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.PredictOptions{Images: []string{"/tmp/image.png"}}
				original := proto.Clone(request)
				Expect(client.PredictStream(ctx, request, func(*pb.Reply) {})).To(Succeed())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "image generation", keyCount: 2, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.GenerateImageRequest{Src: "/tmp/source.png", RefImages: []string{"/tmp/reference.png"}}
				original := proto.Clone(request)
				_, err := client.GenerateImage(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "video generation", keyCount: 3, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.GenerateVideoRequest{StartImage: "/tmp/start.png", EndImage: "/tmp/end.png", Audio: "/tmp/audio.wav"}
				original := proto.Clone(request)
				_, err := client.GenerateVideo(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "3D generation", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.Generate3DRequest{Src: "/tmp/source.glb"}
				original := proto.Clone(request)
				_, err := client.Generate3D(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "TTS", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.TTSRequest{Voice: "/tmp/voice.wav"}
				original := proto.Clone(request)
				_, err := client.TTS(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "streaming TTS", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.TTSRequest{Voice: "/tmp/voice.wav"}
				original := proto.Clone(request)
				Expect(client.TTSStream(ctx, request, func(*pb.Reply) {})).To(Succeed())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "sound generation", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				source := "/tmp/source.wav"
				request := &pb.SoundGenerationRequest{Src: &source}
				original := proto.Clone(request)
				_, err := client.SoundGeneration(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "sound detection", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.SoundDetectionRequest{Src: "/tmp/source.wav"}
				original := proto.Clone(request)
				_, err := client.SoundDetection(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "transcription", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.TranscriptRequest{Dst: "/tmp/source.wav"}
				original := proto.Clone(request)
				_, err := client.AudioTranscription(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
			{name: "streaming transcription", keyCount: 1, invoke: func(client *FileStagingClient) proto.Message {
				request := &pb.TranscriptRequest{Dst: "/tmp/source.wav"}
				original := proto.Clone(request)
				Expect(client.AudioTranscriptionStream(ctx, request, func(*pb.TranscriptStreamResponse) {})).To(Succeed())
				Expect(proto.Equal(request, original)).To(BeTrue())
				return request
			}},
		}

		for _, test := range tests {
			By(test.name)
			stager := &lifecycleStager{}
			client := NewFileStagingClient(&lifecycleBackend{}, stager, "worker-1")
			test.invoke(client)
			Expect(stager.ensureCalls).To(HaveLen(test.keyCount))
			Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
			Expect(stager.releaseBatches).To(Equal([][]string{keysFromEnsureCalls(stager.ensureCalls)}))
			Expect(stager.releaseCtxErr).To(Equal([]error{nil}))
			Expect(stager.releaseHasDeadline).To(HaveLen(1))
			for _, hasDeadline := range stager.releaseHasDeadline {
				Expect(hasDeadline).To(BeTrue())
			}
			for _, deadline := range stager.releaseDeadlines {
				Expect(time.Until(deadline)).To(BeNumerically(">", 0))
				Expect(time.Until(deadline)).To(BeNumerically("<=", time.Minute))
			}
		}
	})

	It("tracks a key before staging so a partial upload failure is released", func(ctx SpecContext) {
		uploadErr := errors.New("upload failed")
		stager := &lifecycleStager{ensureErr: uploadErr, releaseErr: errors.New("release failed")}
		client := NewFileStagingClient(&lifecycleBackend{}, stager, "worker-1")

		_, err := client.GenerateImage(ctx, &pb.GenerateImageRequest{Src: "/tmp/source.png"})

		Expect(err).To(MatchError(ContainSubstring("upload failed")))
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("does not invoke predict when multimodal staging fails", func(ctx SpecContext) {
		uploadErr := errors.New("ephemeral capacity exceeded")
		stager := &lifecycleStager{ensureErr: uploadErr, ensureErrAt: 2}
		backend := &lifecycleBackend{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.PredictOptions{Images: []string{"/tmp/first.png", "/tmp/second.png"}}
		original := proto.Clone(request)

		result, err := client.Predict(ctx, request)

		Expect(result).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("ephemeral capacity exceeded")))
		Expect(backend.predictCalls).To(BeZero())
		Expect(proto.Equal(request, original)).To(BeTrue())
		Expect(stager.ensureCalls).To(HaveLen(2))
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("does not invoke streaming predict when multimodal staging fails", func(ctx SpecContext) {
		uploadErr := errors.New("ephemeral capacity exceeded")
		stager := &lifecycleStager{ensureErr: uploadErr}
		backend := &lifecycleBackend{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.PredictOptions{Audios: []string{"/tmp/audio.wav"}}
		original := proto.Clone(request)

		err := client.PredictStream(ctx, request, func(*pb.Reply) {})

		Expect(err).To(MatchError(ContainSubstring("ephemeral capacity exceeded")))
		Expect(backend.streamCalls).To(BeZero())
		Expect(proto.Equal(request, original)).To(BeTrue())
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("uses an active bounded cleanup context after caller cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stager := &lifecycleStager{}
		client := NewFileStagingClient(&lifecycleBackend{}, stager, "worker-1")

		_, err := client.Predict(ctx, &pb.PredictOptions{Images: []string{"/tmp/image.png"}})

		Expect(err).NotTo(HaveOccurred())
		Expect(stager.releaseCtxErr).To(Equal([]error{nil}))
	})

	It("does not release streaming inputs before the backend completes", func(ctx SpecContext) {
		block := make(chan struct{})
		started := make(chan struct{}, 1)
		backend := &lifecycleBackend{streamBlock: block, streamStarted: started}
		stager := &lifecycleStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		done := make(chan error, 1)

		go func() {
			done <- client.PredictStream(ctx, &pb.PredictOptions{Images: []string{"/tmp/image.png"}}, func(*pb.Reply) {})
		}()

		Eventually(started).Should(Receive())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(stager.releasedKeys).To(BeEmpty())
		close(block)
		Eventually(done).Should(Receive(Succeed()))
		Expect(stager.releasedKeys).To(Equal(keysFromEnsureCalls(stager.ensureCalls)))
	})

	It("does not replace a backend result when cleanup fails", func(ctx SpecContext) {
		reply := &pb.Reply{Message: []byte("ok")}
		backend := &lifecycleBackend{predictResult: reply}
		stager := &lifecycleStager{releaseErr: errors.New("release failed")}
		client := NewFileStagingClient(backend, stager, "worker-1")

		result, err := client.Predict(ctx, &pb.PredictOptions{Images: []string{"/tmp/image.png"}})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeIdenticalTo(reply))
	})
})

func keysFromEnsureCalls(calls []ensureCall) []string {
	keys := make([]string, len(calls))
	for i, call := range calls {
		keys[i] = call.key
	}
	return keys
}
