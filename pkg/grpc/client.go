package grpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/go-skynet/LocalAI/api/schema"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
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

func (c *Client) HealthCheck(ctx context.Context) (bool, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.LoadModel(ctx, in, opts...)
}

func (c *Client) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
		feature, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("Error", err)

			return err
		}
		f(feature.GetMessage())
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
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.GenerateImage(ctx, in, opts...)
}

func (c *Client) TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.TTS(ctx, in, opts...)
}

func (c *Client) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*schema.Result, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	res, err := client.AudioTranscription(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	tresult := &schema.Result{}
	for _, s := range res.Segments {
		tks := []int{}
		for _, t := range s.Tokens {
			tks = append(tks, int(t))
		}
		tresult.Segments = append(tresult.Segments,
			schema.Segment{
				Text:   s.Text,
				Id:     int(s.Id),
				Start:  time.Duration(s.Start),
				End:    time.Duration(s.End),
				Tokens: tks,
			})
	}
	tresult.Text = res.Text
	return tresult, err
}

func (c *Client) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	if c.wd != nil {
		c.wd.Mark(c.address)
		defer c.wd.UnMark(c.address)
	}
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.Status(ctx, &pb.HealthMessage{})
}
