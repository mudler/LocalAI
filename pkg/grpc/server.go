package grpc

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

func (s *server) TTSStream(in *pb.TTSRequest, stream pb.Backend_TTSStreamServer) error {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	audioChan := make(chan []byte)

	done := make(chan bool)
	go func() {
		for audioChunk := range audioChan {
			stream.Send(&pb.Reply{Audio: audioChunk})
		}
		done <- true
	}()

	err := s.llm.TTSStream(in, audioChan)
	<-done

	return err
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

func (s *server) FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest) (*pb.FaceVerifyResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.FaceVerify(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest) (*pb.FaceAnalyzeResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.FaceAnalyze(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest) (*pb.VoiceVerifyResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.VoiceVerify(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest) (*pb.VoiceAnalyzeResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.VoiceAnalyze(in)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *server) VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest) (*pb.VoiceEmbedResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.VoiceEmbed(in)
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
				Text:    s.Text,
				Id:      int32(s.Id),
				Start:   int64(s.Start),
				End:     int64(s.End),
				Tokens:  tks,
				Speaker: s.Speaker,
			})
	}

	tresult.Text = result.Text
	tresult.Language = result.Language
	tresult.Duration = result.Duration
	return tresult, nil
}

func (s *server) AudioTranscriptionStream(in *pb.TranscriptRequest, stream pb.Backend_AudioTranscriptionStreamServer) error {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	resultChan := make(chan *pb.TranscriptStreamResponse)

	done := make(chan bool)
	go func() {
		for chunk := range resultChan {
			stream.Send(chunk)
		}
		done <- true
	}()

	err := s.llm.AudioTranscriptionStream(in, resultChan)
	<-done

	return err
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

func (s *server) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.AudioEncode(in)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *server) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest) (*pb.AudioDecodeResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.AudioDecode(in)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *server) StartFineTune(ctx context.Context, in *pb.FineTuneRequest) (*pb.FineTuneJobResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.StartFineTune(in)
	if err != nil {
		return &pb.FineTuneJobResult{Success: false, Message: fmt.Sprintf("Error starting fine-tune: %s", err.Error())}, err
	}
	return res, nil
}

func (s *server) FineTuneProgress(in *pb.FineTuneProgressRequest, stream pb.Backend_FineTuneProgressServer) error {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	updateChan := make(chan *pb.FineTuneProgressUpdate)

	done := make(chan bool)
	go func() {
		for update := range updateChan {
			stream.Send(update)
		}
		done <- true
	}()

	err := s.llm.FineTuneProgress(in, updateChan)
	<-done

	return err
}

func (s *server) StopFineTune(ctx context.Context, in *pb.FineTuneStopRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.StopFineTune(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error stopping fine-tune: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Fine-tune stopped", Success: true}, nil
}

func (s *server) ListCheckpoints(ctx context.Context, in *pb.ListCheckpointsRequest) (*pb.ListCheckpointsResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.ListCheckpoints(in)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *server) ExportModel(ctx context.Context, in *pb.ExportModelRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.ExportModel(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error exporting model: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Model exported", Success: true}, nil
}

func (s *server) StartQuantization(ctx context.Context, in *pb.QuantizationRequest) (*pb.QuantizationJobResult, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.StartQuantization(in)
	if err != nil {
		return &pb.QuantizationJobResult{Success: false, Message: fmt.Sprintf("Error starting quantization: %s", err.Error())}, err
	}
	return res, nil
}

func (s *server) QuantizationProgress(in *pb.QuantizationProgressRequest, stream pb.Backend_QuantizationProgressServer) error {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	updateChan := make(chan *pb.QuantizationProgressUpdate)

	done := make(chan bool)
	go func() {
		for update := range updateChan {
			stream.Send(update)
		}
		done <- true
	}()

	err := s.llm.QuantizationProgress(in, updateChan)
	<-done

	return err
}

func (s *server) StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest) (*pb.Result, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	err := s.llm.StopQuantization(in)
	if err != nil {
		return &pb.Result{Message: fmt.Sprintf("Error stopping quantization: %s", err.Error()), Success: false}, err
	}
	return &pb.Result{Message: "Quantization stopped", Success: true}, nil
}

func (s *server) ModelMetadata(ctx context.Context, in *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	res, err := s.llm.ModelMetadata(in)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *server) Free(ctx context.Context, in *pb.HealthMessage) (*pb.Result, error) {
	if err := s.llm.Free(); err != nil {
		return &pb.Result{Success: false, Message: err.Error()}, nil
	}
	return &pb.Result{Success: true}, nil
}

// NewBackendServer creates a pb.BackendServer.
func NewBackendServer(model AIModel) pb.BackendServer {
	return &server{llm: model}
}

// AuthTokenEnvVar is the environment variable used to configure gRPC bearer token auth.
const AuthTokenEnvVar = "LOCALAI_GRPC_AUTH_TOKEN"

// validateToken extracts the bearer token from gRPC metadata and validates it.
func validateToken(ctx context.Context, expected string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization header")
	}
	raw := values[0]
	if !strings.HasPrefix(raw, "Bearer ") {
		return status.Error(codes.Unauthenticated, "authorization must use Bearer scheme")
	}
	token := strings.TrimPrefix(raw, "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

func tokenUnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := validateToken(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func tokenStreamInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := validateToken(ss.Context(), token); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// serverOpts returns the common gRPC server options, including auth interceptors
// when LOCALAI_GRPC_AUTH_TOKEN is set.
func serverOpts() []grpc.ServerOption {
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxGRPCMessageSize),
		grpc.MaxSendMsgSize(maxGRPCMessageSize),
	}
	if token := os.Getenv(AuthTokenEnvVar); token != "" {
		opts = append(opts,
			grpc.UnaryInterceptor(tokenUnaryInterceptor(token)),
			grpc.StreamInterceptor(tokenStreamInterceptor(token)),
		)
		log.Printf("gRPC auth enabled via %s", AuthTokenEnvVar)
	}
	return opts
}

func StartServer(address string, model AIModel) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s := grpc.NewServer(serverOpts()...)
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
	s := grpc.NewServer(serverOpts()...)
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
