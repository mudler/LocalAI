package grpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	address  string
	busy     bool
	parallel bool
	sync.Mutex
	opMutex sync.Mutex
	wd      WatchDog
}

type WatchDog interface {
	Mark(address string)
	UnMark(address string)
}

func (c *Client) IsBusy() bool {
	c.Lock()
	defer c.Unlock()
	return c.busy
}

func (c *Client) setBusy(v bool) {
	c.Lock()
	c.busy = v
	c.Unlock()
}

func (c *Client) wdMark() {
	if c.wd != nil {
		c.wd.Mark(c.address)
	}
}

func (c *Client) wdUnMark() {
	if c.wd != nil {
		c.wd.UnMark(c.address)
	}
}

func (c *Client) HealthCheck(ctx context.Context) (bool, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
		reply, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.TTS(ctx, in, opts...)
}

func (c *Client) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.AudioTranscription(ctx, in, opts...)
}

func (c *Client) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Rerank(ctx, in, opts...)
}

func (c *Client) GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...grpc.CallOption) (*pb.MetricsResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
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
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.VAD(ctx, in, opts...)
}

func (c *Client) Detect(ctx context.Context, in *pb.DetectOptions, opts ...grpc.CallOption) (*pb.DetectResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	c.wdMark()
	defer c.wdUnMark()
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024), // 50MB
			grpc.MaxCallSendMsgSize(50*1024*1024), // 50MB
		))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Detect(ctx, in, opts...)
}
