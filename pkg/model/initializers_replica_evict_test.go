// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// unreachableReplica answers the health probe but fails inference with the
// error a refused connection produces.
type unreachableReplica struct{ grpc.Backend }

func (*unreachableReplica) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*unreachableReplica) IsBusy() bool                              { return false }
func (*unreachableReplica) Free(context.Context) error                { return nil }
func (*unreachableReplica) Predict(context.Context, *pb.PredictOptions, ...ggrpc.CallOption) (*pb.Reply, error) {
	return nil, status.Error(codes.Unavailable, "connection refused")
}

type recordingRemoteUnloader struct {
	mu     sync.Mutex
	called []string
}

func (r *recordingRemoteUnloader) UnloadRemoteModel(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.called = append(r.called, name)
	return nil
}

func (r *recordingRemoteUnloader) HasRemoteModel(context.Context, string) (bool, error) {
	return true, nil
}

func (r *recordingRemoteUnloader) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.called...)
}

var _ = Describe("Distributed Load after a replica connection error", func() {
	It("drops only the local stub and does not stop the model on other nodes", func() {
		ml := NewModelLoader(&system.SystemState{})
		unloader := &recordingRemoteUnloader{}
		ml.SetRemoteUnloader(unloader)
		ml.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*Model, error) {
			return NewModelWithClient(id, "replica-a:1", &unreachableReplica{}), nil
		})

		client, err := ml.Load(WithModelID("m1"), WithBackendString("llama-cpp"), WithModel("m1.gguf"))
		Expect(err).ToNot(HaveOccurred())
		_, err = client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())

		Expect(unloader.calls()).To(BeEmpty(),
			"one replica's connection error must not send an unload for the model to every node hosting it")
		Expect(ml.isResident("m1")).To(BeFalse(),
			"the local stub for the failed replica must still be dropped")
	})
})
