package grpc

import (
	"context"
	"fmt"
	"log"
	"net"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

// A GRPC Server that allows to run LLM inference.
// It is used by the LLMServices to expose the LLM functionalities that are called by the client.
// The GRPC Service is general, trying to encompass all the possible LLM options models.
// It depends on the real implementer then what can be done or not.
//
// The server is implemented as a GRPC service, with the following methods:
// - Predict: to run the inference with options
// - PredictStream: to run the inference with options and stream the results

// server is used to implement helloworld.GreeterServer.
type server struct {
	pb.UnimplementedBackendServer
	backend Backend
}

func (s *server) Health(ctx context.Context, in *pb.HealthMessage) (*pb.Reply, error) {
	return newReply("OK"), nil
}

func (s *server) Embedding(ctx context.Context, in *pb.PredictOptions) (*pb.EmbeddingResult, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	embeds, err := s.backend.Embeddings(in)
	if err != nil {
		return nil, err
	}

	return &pb.EmbeddingResult{Embeddings: embeds}, nil
}

func (s *server) LoadModel(ctx context.Context, in *pb.ModelOptions) (*pb.Result, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	err := s.backend.Load(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error loading model: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Loading succeeded", Success: true}, nil
}

func (s *server) Predict(ctx context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	result, err := s.backend.Predict(in)
	return newReply(result), err
}

func (s *server) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest) (*pb.BlobResult, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	blob, err := s.backend.GenerateImage(in)
	if err != nil {
		return &pb.BlobResult{Blob: blob, Message: fmt.Sprintf("Error generating image: %s", err.Error()), Success: false}, err
	}
	return &pb.BlobResult{Blob: blob, Message: "Image generated", Success: true}, nil
}

func (s *server) TTS(ctx context.Context, in *pb.TTSRequest) (*pb.BlobResult, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	b64, err := s.backend.TTS(in)
	if err != nil {
		return &pb.BlobResult{Blob: b64, Message: fmt.Sprintf("Error generating audio: %s", err.Error()), Success: false}, err
	}
	return &pb.BlobResult{Blob: b64, Message: "Audio generated", Success: true}, nil
}

func (s *server) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest) (*pb.TranscriptResult, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	result, err := s.backend.AudioTranscription(in)
	if err != nil {
		return nil, err
	}
	tresult := &pb.TranscriptResult{}
	for _, s := range result.Segments {
		tks := []int32{}
		for _, t := range s.Tokens {
			tks = append(tks, int32(t))
		}
		tresult.Segments = append(tresult.Segments,
			&pb.TranscriptSegment{
				Text:   s.Text,
				Id:     int32(s.Id),
				Start:  int64(s.Start),
				End:    int64(s.End),
				Tokens: tks,
			})
	}

	tresult.Text = result.Text
	return tresult, nil
}

func (s *server) PredictStream(in *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	resultChan := make(chan string)

	done := make(chan bool)
	go func() {
		for result := range resultChan {
			stream.Send(newReply(result))
		}
		done <- true
	}()

	s.backend.PredictStream(in, resultChan)
	<-done

	return nil
}

func (s *server) TokenizeString(ctx context.Context, in *pb.PredictOptions) (*pb.TokenizationResponse, error) {
	if s.backend.Locking() {
		s.backend.Lock()
		defer s.backend.Unlock()
	}
	res, err := s.backend.TokenizeString(in)
	if err != nil {
		return nil, err
	}

	castTokens := make([]int32, len(res.Tokens))
	for i, v := range res.Tokens {
		castTokens[i] = int32(v)
	}

	return &pb.TokenizationResponse{
		Length: int32(res.Length),
		Tokens: castTokens,
	}, err
}

func (s *server) Status(ctx context.Context, in *pb.HealthMessage) (*pb.StatusResponse, error) {
	res, err := s.backend.Status()
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func StartServer(address string, model Backend) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s := grpc.NewServer()
	pb.RegisterBackendServer(s, &server{backend: model})
	log.Printf("gRPC Server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		return err
	}

	return nil
}
