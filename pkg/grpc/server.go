package grpc

import (
	"context"
	"fmt"
	"log"
	"net"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
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
	llm AIModel
}

func (s *server) Health(ctx context.Context, in *pb.HealthMessage) (*pb.Reply, error) {
	return newReply("OK"), nil
}

func (s *server) Embedding(ctx context.Context, in *pb.PredictOptions) (*pb.EmbeddingResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	embeds, err := s.llm.Embeddings(in)
	if err != nil {
		return nil, err
	}

	return &pb.EmbeddingResult{Embeddings: embeds}, nil
}

func (s *server) LoadModel(ctx context.Context, in *pb.ModelOptions) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.Load(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error loading model: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Loading succeeded", Success: true}, nil
}

func (s *server) Predict(ctx context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	result, err := s.llm.Predict(in)
	return newReply(result), err
}

func (s *server) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.GenerateImage(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error generating image: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Image generated", Success: true}, nil
}

func (s *server) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.GenerateVideo(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error generating video: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Video generated", Success: true}, nil
}

func (s *server) TTS(ctx context.Context, in *pb.TTSRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.TTS(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error generating audio: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "TTS audio generated", Success: true}, nil
}

func (s *server) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.SoundGeneration(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error generating audio: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Sound Generation audio generated", Success: true}, nil
}

func (s *server) Detect(ctx context.Context, in *pb.DetectOptions) (*pb.DetectResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.Detect(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest) (*pb.TranscriptResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	result, err := s.llm.AudioTranscription(in)
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
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	resultChan := make(chan string)

	done := make(chan bool)
	go func() {
		for result := range resultChan {
			stream.Send(newReply(result))
		}
		done <- true
	}()

	err := s.llm.PredictStream(in, resultChan)
	<-done

	return err
}

func (s *server) TokenizeString(ctx context.Context, in *pb.PredictOptions) (*pb.TokenizationResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.TokenizeString(in)
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
	res, err := s.llm.Status()
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (s *server) StoresSet(ctx context.Context, in *pb.StoresSetOptions) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.StoresSet(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error setting entry: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Set key", Success: true}, nil
}

func (s *server) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.StoresDelete(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error deleting entry: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Deleted key", Success: true}, nil
}

func (s *server) StoresGet(ctx context.Context, in *pb.StoresGetOptions) (*pb.StoresGetResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.StoresGet(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) StoresFind(ctx context.Context, in *pb.StoresFindOptions) (*pb.StoresFindResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.StoresFind(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) VAD(ctx context.Context, in *pb.VADRequest) (*pb.VADResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.VAD(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func StartServer(address string, model AIModel) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024), // 50MB
		grpc.MaxSendMsgSize(50*1024*1024), // 50MB
	)
	pb.RegisterBackendServer(s, &server{llm: model})
	log.Printf("gRPC Server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		return err
	}

	return nil
}

func RunServer(address string, model AIModel) (func() error, error) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024), // 50MB
		grpc.MaxSendMsgSize(50*1024*1024), // 50MB
	)
	pb.RegisterBackendServer(s, &server{llm: model})
	log.Printf("gRPC Server listening at %v", lis.Addr())
	if err = s.Serve(lis); err != nil {
		return func() error {
			return lis.Close()
		}, err
	}

	return func() error {
		s.GracefulStop()
		return nil
	}, nil
}
