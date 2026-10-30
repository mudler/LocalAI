// SPDX-License-Identifier: MIT

// Command fakejobs is a gRPC backend that stands in for the training and
// quantization engines in the shipped add-on suite. A job it starts reports
// "training" or "quantizing" until it is stopped, or until a file named
// <job id>.finish appears in the directory named by FAKEJOBS_DIR, then it
// reports "completed". It does no work.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

var addr = flag.String("addr", "localhost:50051", "address to listen on")

type backend struct {
	pb.UnimplementedBackendServer
	mu      sync.Mutex
	stopped map[string]bool
}

func (b *backend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (b *backend) LoadModel(context.Context, *pb.ModelOptions) (*pb.Result, error) {
	return &pb.Result{Success: true, Message: "ok"}, nil
}

func (b *backend) isStopped(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopped[id]
}

func (b *backend) stop(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped[id] = true
}

func finished(id string) bool {
	dir := os.Getenv("FAKEJOBS_DIR") //nolint:forbidigo // test add-on executable; the spec passes its folder through the environment
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, id+".finish")) // #nosec G304 -- test backend reads its own directory
	return err == nil
}

func (b *backend) StartQuantization(_ context.Context, in *pb.QuantizationRequest) (*pb.QuantizationJobResult, error) {
	return &pb.QuantizationJobResult{JobId: in.JobId, Success: true, Message: "started"}, nil
}

func (b *backend) QuantizationProgress(in *pb.QuantizationProgressRequest, stream pb.Backend_QuantizationProgressServer) error {
	for {
		status, pct := "quantizing", float32(40)
		switch {
		case b.isStopped(in.JobId):
			status, pct = "stopped", 40
		case finished(in.JobId):
			status, pct = "completed", 100
		}
		if err := stream.Send(&pb.QuantizationProgressUpdate{JobId: in.JobId, ProgressPercent: pct, Status: status, Message: status}); err != nil {
			return err
		}
		if status != "quantizing" {
			return nil
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (b *backend) StopQuantization(_ context.Context, in *pb.QuantizationStopRequest) (*pb.Result, error) {
	b.stop(in.JobId)
	return &pb.Result{Success: true}, nil
}

func (b *backend) StartFineTune(_ context.Context, in *pb.FineTuneRequest) (*pb.FineTuneJobResult, error) {
	return &pb.FineTuneJobResult{JobId: in.JobId, Success: true, Message: "started"}, nil
}

func (b *backend) FineTuneProgress(in *pb.FineTuneProgressRequest, stream pb.Backend_FineTuneProgressServer) error {
	for {
		status, pct := "training", float32(40)
		switch {
		case b.isStopped(in.JobId):
			status = "stopped"
		case finished(in.JobId):
			status, pct = "completed", 100
		}
		if err := stream.Send(&pb.FineTuneProgressUpdate{JobId: in.JobId, ProgressPercent: pct, Status: status, Message: status, CurrentStep: 4, TotalSteps: 10}); err != nil {
			return err
		}
		if status != "training" {
			return nil
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (b *backend) StopFineTune(_ context.Context, in *pb.FineTuneStopRequest) (*pb.Result, error) {
	b.stop(in.JobId)
	return &pb.Result{Success: true}, nil
}

func main() {
	flag.Parse()
	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterBackendServer(s, &backend{stopped: map[string]bool{}})
	if err := s.Serve(l); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
