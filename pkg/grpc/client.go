package grpc

import (
	"context"
	"fmt"
	"io"
	"time"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	address string
}

func NewClient(address string) *Client {
	return &Client{
		address: address,
	}
}

func (c *Client) HealthCheck(ctx context.Context) bool {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Println(err)
		return false
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	// The healthcheck call shouldn't take long time
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := client.Health(ctx, &pb.HealthMessage{})
	if err != nil {
		fmt.Println(err)

		return false
	}

	if string(res.Message) == "OK" {
		return true
	}
	return false
}

func (c *Client) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	return client.Embedding(ctx, in, opts...)
}

func (c *Client) Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	return client.Predict(ctx, in, opts...)
}

func (c *Client) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.LoadModel(ctx, in, opts...)
}

func (c *Client) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error {
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
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.GenerateImage(ctx, in, opts...)
}

func (c *Client) TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)
	return client.TTS(ctx, in, opts...)
}

func (c *Client) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*api.Result, error) {
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
	tresult := &api.Result{}
	for _, s := range res.Segments {
		tks := []int{}
		for _, t := range s.Tokens {
			tks = append(tks, int(t))
		}
		tresult.Segments = append(tresult.Segments,
			api.Segment{
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

func (c *Client) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*TokenizationResponse, error) {
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

	castTokens := make([]int, len(res.Tokens))
	for i, v := range res.Tokens {
		castTokens[i] = int(v)
	}

	return &TokenizationResponse{
		Length: int(res.Length),
		Tokens: castTokens,
	}, err
}
