package grpc

import (
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type LLM interface {
	Predict(*pb.PredictOptions) (string, error)
	PredictStream(*pb.PredictOptions, chan string)
	Load(*pb.ModelOptions) error
}
