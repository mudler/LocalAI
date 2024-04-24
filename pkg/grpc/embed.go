package grpc

import (
	"context"
	"time"

	"github.com/go-skynet/LocalAI/core/schema"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var _ Backend = new(embedBackend)
var _ pb.Backend_PredictStreamServer = new(embedBackendServerStream)

type embedBackend struct {
	s *server
}

func (e *embedBackend) IsBusy() bool {
	return e.s.llm.Busy()
}

func (e *embedBackend) HealthCheck(ctx context.Context) (bool, error) {
	return true, nil
}

func (e *embedBackend) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error) {
	return e.s.Embedding(ctx, in)
}

func (e *embedBackend) Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error) {
	return e.s.Predict(ctx, in)
}

func (e *embedBackend) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.LoadModel(ctx, in)
}

func (e *embedBackend) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error {
	bs := &embedBackendServerStream{
		ctx: ctx,
		fn:  f,
	}
	return e.s.PredictStream(in, bs)
}

func (e *embedBackend) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.GenerateImage(ctx, in)
}

func (e *embedBackend) TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.TTS(ctx, in)
}

func (e *embedBackend) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*schema.TranscriptionResult, error) {
	r, err := e.s.AudioTranscription(ctx, in)
	if err != nil {
		return nil, err
	}
	tr := &schema.TranscriptionResult{}
	for _, s := range r.Segments {
		var tks []int
		for _, t := range s.Tokens {
			tks = append(tks, int(t))
		}
		tr.Segments = append(tr.Segments,
			schema.Segment{
				Text:   s.Text,
				Id:     int(s.Id),
				Start:  time.Duration(s.Start),
				End:    time.Duration(s.End),
				Tokens: tks,
			})
	}
	tr.Text = r.Text
	return tr, err
}

func (e *embedBackend) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error) {
	return e.s.TokenizeString(ctx, in)
}

func (e *embedBackend) Status(ctx context.Context) (*pb.StatusResponse, error) {
	return e.s.Status(ctx, &pb.HealthMessage{})
}

func (e *embedBackend) StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StoresSet(ctx, in)
}

func (e *embedBackend) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StoresDelete(ctx, in)
}

func (e *embedBackend) StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...grpc.CallOption) (*pb.StoresGetResult, error) {
	return e.s.StoresGet(ctx, in)
}

func (e *embedBackend) StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...grpc.CallOption) (*pb.StoresFindResult, error) {
	return e.s.StoresFind(ctx, in)
}

func (e *embedBackend) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...grpc.CallOption) (*pb.RerankResult, error) {
	return e.s.Rerank(ctx, in)
}

type embedBackendServerStream struct {
	ctx context.Context
	fn  func(s []byte)
}

func (e *embedBackendServerStream) Send(reply *pb.Reply) error {
	e.fn(reply.GetMessage())
	return nil
}

func (e *embedBackendServerStream) SetHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendServerStream) SendHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendServerStream) SetTrailer(md metadata.MD) {
}

func (e *embedBackendServerStream) Context() context.Context {
	return e.ctx
}

func (e *embedBackendServerStream) SendMsg(m any) error {
	if x, ok := m.(*pb.Reply); ok {
		return e.Send(x)
	}
	return nil
}

func (e *embedBackendServerStream) RecvMsg(m any) error {
	return nil
}
