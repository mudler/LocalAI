package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const maxGRPCMessageSize = 50 * 1024 * 1024 // 50MB

// bearerToken implements credentials.PerRPCCredentials to inject a bearer token
// into every gRPC call.
type bearerToken struct {
	token string
}

func (b bearerToken) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b bearerToken) RequireTransportSecurity() bool { return false }

type Client struct {
	address  string
	inFlight int
	parallel bool
	token    string
	sync.Mutex
	opMutex sync.Mutex
	wd      WatchDog
}

type WatchDog interface {
	TrackRequest(address string) func()
}

func (c *Client) IsBusy() bool {
	c.Lock()
	defer c.Unlock()
	return c.inFlight > 0
}

// setBusy preserves the existing call-site shape while maintaining a count.
// Parallel requests can finish in any order, so a boolean would let the first
// completion report the backend idle while other calls were still running.
func (c *Client) setBusy(v bool) {
	c.Lock()
	if v {
		c.inFlight++
	} else if c.inFlight > 0 {
		c.inFlight--
	}
	c.Unlock()
}

func (c *Client) wdMark() func() {
	if c.wd != nil {
		return c.wd.TrackRequest(c.address)
	}
	return func() {}
}

// dial creates a gRPC client connection with common options.
// If c.token is set, bearer token credentials are included.
func (c *Client) dial() (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageSize),
			grpc.MaxCallSendMsgSize(maxGRPCMessageSize),
		),
	}
	if c.token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(bearerToken{token: c.token}))
	}
	return grpc.NewClient(c.address, opts...)
}

func (c *Client) HealthCheck(ctx context.Context) (bool, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := c.dial()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	// The healthcheck call shouldn't take long time
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := client.Health(ctx, &pb.HealthMessage{})
	if err != nil {
		return false, err
	}

	if string(res.Message) == "OK" {
		return true, nil
	}

	return false, fmt.Errorf("health check failed: %s", res.Message)
}

func (c *Client) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	return client.Embedding(ctx, in, opts...)
}

func (c *Client) Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	return client.Predict(ctx, in, opts...)
}

func (c *Client) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.LoadModel(ctx, in, opts...)
}

func (c *Client) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	stream, err := client.PredictStream(ctx, in, opts...)
	if err != nil {
		return err
	}

	for {
		// Check if context is cancelled before receiving
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		reply, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Println("Error", err)

			return err
		}
		f(reply)
	}

	return nil
}

func (c *Client) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.GenerateImage(ctx, in, opts...)
}

func (c *Client) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.GenerateVideo(ctx, in, opts...)
}

func (c *Client) TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.TTS(ctx, in, opts...)
}

func (c *Client) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	stream, err := client.TTSStream(ctx, in, opts...)
	if err != nil {
		return err
	}

	for {
		// Check if context is cancelled before receiving
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		reply, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		f(reply)
	}

	return nil
}

func (c *Client) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.SoundGeneration(ctx, in, opts...)
}

func (c *Client) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*pb.TranscriptResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.AudioTranscription(ctx, in, opts...)
}

func (c *Client) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	stream, err := client.AudioTranscriptionStream(ctx, in, opts...)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		f(chunk)
	}

	return nil
}

func (c *Client) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	res, err := client.TokenizeString(ctx, in, opts...)

	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) Status(ctx context.Context) (*pb.StatusResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Status(ctx, &pb.HealthMessage{})
}

func (c *Client) StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StoresSet(ctx, in, opts...)
}

func (c *Client) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	defer c.wdMark()()
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StoresDelete(ctx, in, opts...)
}

func (c *Client) StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...grpc.CallOption) (*pb.StoresGetResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StoresGet(ctx, in, opts...)
}

func (c *Client) StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...grpc.CallOption) (*pb.StoresFindResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StoresFind(ctx, in, opts...)
}

func (c *Client) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...grpc.CallOption) (*pb.RerankResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Rerank(ctx, in, opts...)
}

func (c *Client) TokenClassify(ctx context.Context, in *pb.TokenClassifyRequest, opts ...grpc.CallOption) (*pb.TokenClassifyResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.TokenClassify(ctx, in, opts...)
}

func (c *Client) Score(ctx context.Context, in *pb.ScoreRequest, opts ...grpc.CallOption) (*pb.ScoreResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.Score(ctx, in, opts...)
}

func (c *Client) GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...grpc.CallOption) (*pb.MetricsResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.GetMetrics(ctx, in, opts...)
}

func (c *Client) VAD(ctx context.Context, in *pb.VADRequest, opts ...grpc.CallOption) (*pb.VADResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.VAD(ctx, in, opts...)
}

func (c *Client) Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...grpc.CallOption) (*pb.DiarizeResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.Diarize(ctx, in, opts...)
}

func (c *Client) SoundDetection(ctx context.Context, in *pb.SoundDetectionRequest, opts ...grpc.CallOption) (*pb.SoundDetectionResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.SoundDetection(ctx, in, opts...)
}

func (c *Client) Detect(ctx context.Context, in *pb.DetectOptions, opts ...grpc.CallOption) (*pb.DetectResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Detect(ctx, in, opts...)
}

func (c *Client) Depth(ctx context.Context, in *pb.DepthRequest, opts ...grpc.CallOption) (*pb.DepthResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.Depth(ctx, in, opts...)
}

func (c *Client) FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest, opts ...grpc.CallOption) (*pb.FaceVerifyResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.FaceVerify(ctx, in, opts...)
}

func (c *Client) FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest, opts ...grpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.FaceAnalyze(ctx, in, opts...)
}

func (c *Client) VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...grpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.VoiceVerify(ctx, in, opts...)
}

func (c *Client) VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...grpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.VoiceAnalyze(ctx, in, opts...)
}

func (c *Client) VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...grpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.VoiceEmbed(ctx, in, opts...)
}

func (c *Client) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...grpc.CallOption) (*pb.AudioEncodeResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.AudioEncode(ctx, in, opts...)
}

func (c *Client) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...grpc.CallOption) (*pb.AudioDecodeResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.AudioDecode(ctx, in, opts...)
}

func (c *Client) AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...grpc.CallOption) (*pb.AudioTransformResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewBackendClient(conn)
	return client.AudioTransform(ctx, in, opts...)
}

// ForwardClient is the duplex interface returned by (*Client).Forward.
// First Send carries path/method/headers/body, subsequent Sends carry
// body_chunk only. First Recv carries status/headers, subsequent Recvs
// carry body_chunk. Caller closes via CloseSend when request is done;
// stream ends when the upstream finishes and the server closes.
type ForwardClient interface {
	Send(*pb.ForwardRequest) error
	Recv() (*pb.ForwardReply, error)
	CloseSend() error
	Context() context.Context
}

type forwardClient struct {
	pb.Backend_ForwardClient
	conn   *grpc.ClientConn
	closer func()
	once   sync.Once
}

// CloseSend signals end-of-requests to the server but keeps the
// underlying connection open so the server can still send replies.
// Connection cleanup happens when Recv returns a final error (EOF
// or any other terminal status).
func (s *forwardClient) CloseSend() error {
	return s.Backend_ForwardClient.CloseSend()
}

// Recv wraps the embedded stream's Recv to fire the connection-level
// closer once the stream ends. On EOF or any other error the
// connection + operation-state cleanup runs exactly once.
func (s *forwardClient) Recv() (*pb.ForwardReply, error) {
	reply, err := s.Backend_ForwardClient.Recv()
	if err != nil && s.closer != nil {
		s.once.Do(s.closer)
	}
	return reply, err
}

func (c *Client) Forward(ctx context.Context, opts ...grpc.CallOption) (ForwardClient, error) {
	if !c.parallel {
		c.opMutex.Lock()
	}
	c.setBusy(true)
	completeRequest := c.wdMark()

	cleanup := func() {
		completeRequest()
		c.setBusy(false)
		if !c.parallel {
			c.opMutex.Unlock()
		}
	}

	conn, err := c.dial()
	if err != nil {
		cleanup()
		return nil, err
	}
	client := pb.NewBackendClient(conn)
	stream, err := client.Forward(ctx, opts...)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, err
	}
	return &forwardClient{
		Backend_ForwardClient: stream,
		conn:                  conn,
		closer: func() {
			_ = conn.Close()
			cleanup()
		},
	}, nil
}

// AudioTransformStreamClient is the duplex interface returned by
// (*Client).AudioTransformStream. Wraps the generated bidi client without
// leaking the proto package across the public boundary.
type AudioTransformStreamClient interface {
	Send(*pb.AudioTransformFrameRequest) error
	Recv() (*pb.AudioTransformFrameResponse, error)
	CloseSend() error
	Context() context.Context
}

// audioTransformStreamClient is the concrete wrapper. It also owns the
// underlying gRPC connection, released once the receive side terminates —
// NOT at CloseSend, because the server still streams responses (the tail of
// the transform) after the client closes its send side. Same lifecycle as
// forwardClient.
type audioTransformStreamClient struct {
	pb.Backend_AudioTransformStreamClient
	closeOnce sync.Once
	closer    func()
}

func (s *audioTransformStreamClient) Recv() (*pb.AudioTransformFrameResponse, error) {
	resp, err := s.Backend_AudioTransformStreamClient.Recv()
	if err != nil && s.closer != nil {
		s.closeOnce.Do(s.closer)
	}
	return resp, err
}

func (c *Client) AudioTransformStream(ctx context.Context, opts ...grpc.CallOption) (AudioTransformStreamClient, error) {
	if !c.parallel {
		c.opMutex.Lock()
	}
	c.setBusy(true)
	completeRequest := c.wdMark()

	cleanup := func() {
		completeRequest()
		c.setBusy(false)
		if !c.parallel {
			c.opMutex.Unlock()
		}
	}

	conn, err := c.dial()
	if err != nil {
		cleanup()
		return nil, err
	}
	client := pb.NewBackendClient(conn)
	stream, err := client.AudioTransformStream(ctx, opts...)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, err
	}
	return &audioTransformStreamClient{
		Backend_AudioTransformStreamClient: stream,
		closer: func() {
			_ = conn.Close()
			cleanup()
		},
	}, nil
}

// AudioTranscriptionLiveClient is the duplex interface returned by
// (*Client).AudioTranscriptionLive. Wraps the generated bidi client without
// leaking the proto package across the public boundary.
type AudioTranscriptionLiveClient interface {
	Send(*pb.TranscriptLiveRequest) error
	Recv() (*pb.TranscriptLiveResponse, error)
	CloseSend() error
	Context() context.Context
}

type audioTranscriptionLiveClient struct {
	pb.Backend_AudioTranscriptionLiveClient
	closeOnce sync.Once
	closer    func()
}

// Recv releases the connection once the stream reaches a terminal state
// (io.EOF after the server finishes, or any error). The conn MUST survive
// CloseSend: the live protocol is close-send -> backend flushes the decode
// tail -> terminal FinalResult arrives. Closing the conn inside CloseSend
// killed that pending Recv with "grpc: the client connection is closing",
// losing the final transcript (and its tail words) on every turn.
func (s *audioTranscriptionLiveClient) Recv() (*pb.TranscriptLiveResponse, error) {
	resp, err := s.Backend_AudioTranscriptionLiveClient.Recv()
	if err != nil {
		s.release()
	}
	return resp, err
}

func (s *audioTranscriptionLiveClient) release() {
	s.closeOnce.Do(func() {
		if s.closer != nil {
			s.closer()
		}
	})
}

// AudioTranscriptionLive opens the bidirectional live ASR stream. Note the
// same caveat as AudioToAudioStream: the watchdog busy-mark (and, on
// non-parallel backends, opMutex) is held for the stream's lifetime, which
// for a realtime session can be minutes — enable parallel requests on
// backends meant to serve live sessions alongside unary work.
func (c *Client) AudioTranscriptionLive(ctx context.Context, opts ...grpc.CallOption) (AudioTranscriptionLiveClient, error) {
	if !c.parallel {
		c.opMutex.Lock()
	}
	c.setBusy(true)
	completeRequest := c.wdMark()

	cleanup := func() {
		completeRequest()
		c.setBusy(false)
		if !c.parallel {
			c.opMutex.Unlock()
		}
	}

	conn, err := c.dial()
	if err != nil {
		cleanup()
		return nil, err
	}
	client := pb.NewBackendClient(conn)
	stream, err := client.AudioTranscriptionLive(ctx, opts...)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, err
	}
	return &audioTranscriptionLiveClient{
		Backend_AudioTranscriptionLiveClient: stream,
		closer: func() {
			_ = conn.Close()
			cleanup()
		},
	}, nil
}

// AudioToAudioStreamClient is the duplex interface returned by
// (*Client).AudioToAudioStream. Mirrors AudioTransformStreamClient's
// shape so realtime-API callers can plug in interchangeable backends.
type AudioToAudioStreamClient interface {
	Send(*pb.AudioToAudioRequest) error
	Recv() (*pb.AudioToAudioResponse, error)
	CloseSend() error
	Context() context.Context
}

// audioToAudioStreamClient owns its gRPC connection, released once the
// receive side terminates — NOT at CloseSend, because the server still
// streams the response tail after the client closes its send side. Same
// lifecycle as forwardClient.
type audioToAudioStreamClient struct {
	pb.Backend_AudioToAudioStreamClient
	closeOnce sync.Once
	closer    func()
}

func (s *audioToAudioStreamClient) Recv() (*pb.AudioToAudioResponse, error) {
	resp, err := s.Backend_AudioToAudioStreamClient.Recv()
	if err != nil && s.closer != nil {
		s.closeOnce.Do(s.closer)
	}
	return resp, err
}

func (c *Client) AudioToAudioStream(ctx context.Context, opts ...grpc.CallOption) (AudioToAudioStreamClient, error) {
	if !c.parallel {
		c.opMutex.Lock()
	}
	c.setBusy(true)
	completeRequest := c.wdMark()

	cleanup := func() {
		completeRequest()
		c.setBusy(false)
		if !c.parallel {
			c.opMutex.Unlock()
		}
	}

	conn, err := c.dial()
	if err != nil {
		cleanup()
		return nil, err
	}
	client := pb.NewBackendClient(conn)
	stream, err := client.AudioToAudioStream(ctx, opts...)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, err
	}
	return &audioToAudioStreamClient{
		Backend_AudioToAudioStreamClient: stream,
		closer: func() {
			_ = conn.Close()
			cleanup()
		},
	}, nil
}

func (c *Client) StartFineTune(ctx context.Context, in *pb.FineTuneRequest, opts ...grpc.CallOption) (*pb.FineTuneJobResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StartFineTune(ctx, in, opts...)
}

func (c *Client) FineTuneProgress(ctx context.Context, in *pb.FineTuneProgressRequest, f func(update *pb.FineTuneProgressUpdate), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	stream, err := client.FineTuneProgress(ctx, in, opts...)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		update, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		f(update)
	}

	return nil
}

func (c *Client) StopFineTune(ctx context.Context, in *pb.FineTuneStopRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StopFineTune(ctx, in, opts...)
}

func (c *Client) ListCheckpoints(ctx context.Context, in *pb.ListCheckpointsRequest, opts ...grpc.CallOption) (*pb.ListCheckpointsResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.ListCheckpoints(ctx, in, opts...)
}

func (c *Client) ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.ExportModel(ctx, in, opts...)
}

func (c *Client) StartQuantization(ctx context.Context, in *pb.QuantizationRequest, opts ...grpc.CallOption) (*pb.QuantizationJobResult, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StartQuantization(ctx, in, opts...)
}

func (c *Client) QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, f func(update *pb.QuantizationProgressUpdate), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	stream, err := client.QuantizationProgress(ctx, in, opts...)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		update, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		f(update)
	}

	return nil
}

func (c *Client) StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.StopQuantization(ctx, in, opts...)
}

func (c *Client) Free(ctx context.Context) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()

	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pb.NewBackendClient(conn)
	_, err = client.Free(ctx, &pb.HealthMessage{})
	return err
}

func (c *Client) ModelMetadata(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.ModelMetadataResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.ModelMetadata(ctx, in, opts...)
}
