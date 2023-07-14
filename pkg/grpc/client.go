package grpc

import (
	"context"
	"fmt"
	"io"
	"time"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
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
	client := pb.NewLLMClient(conn)

	// The healthcheck call shouldn't take long time
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := client.Health(ctx, &pb.HealthMessage{})
	if err != nil {
		fmt.Println(err)

		return false
	}

	if res.Message == "OK" {
		return true
	}
	return false
}

func (c *Client) Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewLLMClient(conn)

	return client.Predict(ctx, in, opts...)
}

func (c *Client) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := pb.NewLLMClient(conn)
	return client.LoadModel(ctx, in, opts...)
}

func (c *Client) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(s string), opts ...grpc.CallOption) error {
	conn, err := grpc.Dial(c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewLLMClient(conn)

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
