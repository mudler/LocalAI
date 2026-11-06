package grpc

import (
	"context"
	"math"
	"net"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	g "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type musicProbe struct {
	base.SingleThread
	generation func(context.Context, *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error)
}

func (p *musicProbe) MusicGeneration(ctx context.Context, r *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
	defer GinkgoRecover()
	return p.generation(ctx, r)
}
func (p *musicProbe) MusicTranscription(ctx context.Context, r *pb.MusicTranscriptionRequest) (*pb.MusicTranscriptionResponse, error) {
	return &pb.MusicTranscriptionResponse{Abc: r.RequestId + ":" + r.TrustedSource}, nil
}
func musicRequest() *pb.MusicGenerationRequest {
	return &pb.MusicGenerationRequest{RequestId: "opaque-id", ModelIdentity: "loaded", Style: "jazz", Lyrics: "verse\nritornello é", TrustedOutputDestination: "/private/request/result.wav", Params: &pb.MusicGenerationParams{Cot: pb.MusicCOT_MUSIC_COT_FULL, Seed: 2147483647, GuidanceScale: 0, NumInferenceSteps: 100}}
}

var _ = Describe("Typed music adapter", func() {
	It("preserves wire fields, presence, cancellation, and deadlines", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		probe := &musicProbe{}
		srv := &server{llm: probe, loadedIdentity: "loaded"}
		wire := g.NewServer()
		pb.RegisterBackendServer(wire, srv)
		go wire.Serve(listener)
		defer wire.Stop()
		client := &Client{address: listener.Addr().String(), parallel: true}
		for _, abc := range []*string{nil, proto.String("X:1\nK:C\nC D E|\n")} {
			request := musicRequest()
			request.Abc = abc
			probe.generation = func(ctx context.Context, got *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
				defer GinkgoRecover()
				Expect(proto.Equal(got, request)).To(BeTrue(), "all typed fields/presence changed")
				_, ok := ctx.Deadline()
				Expect(ok).To(BeTrue(), "deadline dropped")
				return &pb.MusicGenerationResponse{Success: true, GeneratedAbc: proto.String("")}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			response, err := client.MusicGeneration(ctx, request)
			cancel()
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.Success).To(BeTrue())
			Expect(response.GeneratedAbc).NotTo(BeNil())
			Expect(*response.GeneratedAbc).To(BeEmpty())
		}
		// Wire preserves present-empty independently from validation rejecting it.
		present := musicRequest()
		present.Abc = proto.String("")
		data, _ := proto.Marshal(present)
		var decoded pb.MusicGenerationRequest
		Expect(proto.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded.Abc).NotTo(BeNil(), "empty-present ABC lost")
		_, err = client.MusicGeneration(context.Background(), present)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "empty-present accepted")
		entered := make(chan struct{})
		settled := make(chan struct{})
		probe.generation = func(ctx context.Context, _ *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
			defer GinkgoRecover()
			close(entered)
			<-ctx.Done()
			close(settled)
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); _, err := client.MusicGeneration(ctx, musicRequest()); done <- err }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			Fail("RPC did not enter")
		}
		cancel()
		select {
		case err := <-done:
			Expect(status.Code(err)).To(Equal(codes.Canceled))
		case <-time.After(time.Second):
			Fail("client context dropped")
		}
		select {
		case <-settled:
		case <-time.After(time.Second):
			Fail("model context dropped")
		}
		deadlineEntered := make(chan struct{})
		deadlineSettled := make(chan struct{})
		probe.generation = func(ctx context.Context, _ *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
			defer GinkgoRecover()
			close(deadlineEntered)
			<-ctx.Done()
			close(deadlineSettled)
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, deadlineErr := client.MusicGeneration(deadlineCtx, musicRequest())
		deadlineCancel()
		Expect(status.Code(deadlineErr)).To(Equal(codes.DeadlineExceeded), "deadline did not propagate")
		select {
		case <-deadlineSettled:
		case <-time.After(time.Second):
			Fail("model deadline lost")
		}
		request := &pb.MusicTranscriptionRequest{RequestId: "transcribe", ModelIdentity: "loaded", TrustedSource: "/private/source.wav"}
		result, err := client.MusicTranscription(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Abc).To(Equal("transcribe:/private/source.wav"))
	})
	It("validates explicit parameters, optional dispatch, and strict identity", func() {
		Expect(validateMusicGeneration(musicRequest())).To(Succeed(), "guidance zero rejected")
		mutations := []func(*pb.MusicGenerationRequest){func(r *pb.MusicGenerationRequest) { r.Params = nil }, func(r *pb.MusicGenerationRequest) { r.Abc = proto.String("") }, func(r *pb.MusicGenerationRequest) { r.Params.Cot = 0 }, func(r *pb.MusicGenerationRequest) { r.Params.Seed = 2147483648 }, func(r *pb.MusicGenerationRequest) { r.Params.GuidanceScale = math.NaN() }, func(r *pb.MusicGenerationRequest) { r.Params.GuidanceScale = 21 }, func(r *pb.MusicGenerationRequest) { r.Params.NumInferenceSteps = 0 }, func(r *pb.MusicGenerationRequest) { r.ModelIdentity = "" }, func(r *pb.MusicGenerationRequest) { r.Abc = proto.String("%%include /etc/passwd") }, func(r *pb.MusicGenerationRequest) {
			r.Abc = proto.String("X:1\nK:C\nC")
			r.Params.Cot = pb.MusicCOT_MUSIC_COT_OFF
		}, func(r *pb.MusicGenerationRequest) { r.Lyrics = string([]byte{255}) }}
		for i, mutate := range mutations {
			r := musicRequest()
			mutate(r)
			Expect(status.Code(validateMusicGeneration(r))).To(Equal(codes.InvalidArgument), "invalid case %d accepted", i)
		}
		srv := &server{llm: &base.SingleThread{}, loadedIdentity: "loaded"}
		embedded := &embedBackend{s: srv}
		_, err := embedded.MusicGeneration(context.Background(), musicRequest())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented), "optional model fell back")
		for _, identity := range []string{"", "stale"} {
			srv.loadedIdentity = identity
			_, err := embedded.MusicGeneration(context.Background(), musicRequest())
			Expect(status.Code(err)).To(Equal(map[bool]codes.Code{true: codes.FailedPrecondition, false: codes.NotFound}[identity == ""]), "strict identity skipped")
		}
		probe := &musicProbe{}
		srv.llm = probe
		srv.loadedIdentity = "loaded"
		key := struct{}{}
		ctx := context.WithValue(context.Background(), key, "same-context")
		probe.generation = func(got context.Context, _ *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
			defer GinkgoRecover()
			Expect(got).To(BeIdenticalTo(ctx), "embedded substituted context")
			return &pb.MusicGenerationResponse{}, nil
		}
		_, err = embedded.MusicGeneration(ctx, musicRequest())
		Expect(err).NotTo(HaveOccurred())
	})

	It("returns wire UNIMPLEMENTED without legacy fallback and rejects stale identities", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		wire := g.NewServer()
		pb.RegisterBackendServer(wire, &pb.UnimplementedBackendServer{})
		go wire.Serve(listener)
		defer wire.Stop()
		client := &Client{address: listener.Addr().String(), parallel: true}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err = client.MusicGeneration(ctx, musicRequest())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented), "unknown server RPC")
		_, err = client.MusicTranscription(ctx, &pb.MusicTranscriptionRequest{RequestId: "id", ModelIdentity: "loaded", TrustedSource: "/private/source.wav"})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented), "unknown transcription RPC")
		for _, identity := range []string{"loaded", "", "stale"} {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			serverWire := g.NewServer()
			pb.RegisterBackendServer(serverWire, &server{llm: &base.SingleThread{}, loadedIdentity: identity})
			go serverWire.Serve(listener)
			func() {
				defer serverWire.Stop()
				client := &Client{address: listener.Addr().String(), parallel: true}
				expected := codes.Unimplemented
				if identity == "" {
					expected = codes.FailedPrecondition
				} else if identity == "stale" {
					expected = codes.NotFound
				}
				_, err := client.MusicGeneration(ctx, musicRequest())
				Expect(status.Code(err)).To(Equal(expected), "optional/identity generation wire")
				_, err = client.MusicTranscription(ctx, &pb.MusicTranscriptionRequest{RequestId: "id", ModelIdentity: "loaded", TrustedSource: "/private/source.wav"})
				Expect(status.Code(err)).To(Equal(expected), "optional/identity transcription wire")
			}()
		}
	})

	It("enforces inclusive text and parameter caps", func() {
		for _, n := range []int{4096, 4097} {
			r := musicRequest()
			r.Style = strings.Repeat("s", n)
			err := validateMusicGeneration(r)
			Expect(err == nil).To(Equal(n == 4096), "style cap %d", n)
		}
		for _, n := range []int{16384, 16385} {
			r := musicRequest()
			r.Lyrics = strings.Repeat("l", n)
			err := validateMusicGeneration(r)
			Expect(err == nil).To(Equal(n == 16384), "lyrics cap %d", n)
		}
		for _, n := range []int{262144, 262145} {
			r := musicRequest()
			r.Abc = proto.String(strings.Repeat("C", n))
			err := validateMusicGeneration(r)
			Expect(err == nil).To(Equal(n == 262144), "ABC cap %d", n)
		}
		for _, n := range []uint32{1, 100, 101} {
			r := musicRequest()
			r.Params.NumInferenceSteps = n
			Expect(validateMusicGeneration(r) == nil).To(Equal(n <= 100), "step cap %d", n)
		}
		for _, v := range []float64{-1, 0, 20, 21, math.Inf(1)} {
			r := musicRequest()
			r.Params.GuidanceScale = v
			Expect(validateMusicGeneration(r) == nil).To(Equal(v >= 0 && v <= 20), "guidance cap %v", v)
		}
	})
})
