// SPDX-License-Identifier: MIT

package facerecognition

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

func TestEnrollmentReplay(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Enrollment replay") }

type replayStore struct {
	grpc.Backend
	mu      sync.Mutex
	entries map[string][]byte
}

func (s *replayStore) StoresSet(_ context.Context, in *pb.StoresSetOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range in.Keys {
		b, _ := json.Marshal(k.Floats)
		s.entries[string(b)] = append([]byte(nil), in.Values[i].Bytes...)
	}
	return &pb.Result{Success: true}, nil
}

var _ = Describe("Enrollment replay", func() {
	It("keeps the identity across registry instances and a cleared store", func(ctx SpecContext) {
		storage := &replayStore{entries: map[string][]byte{}}
		newRegistry := func() Registry {
			return NewStoreRegistry(func(context.Context, string) (grpc.Backend, error) { return storage, nil }, "faces", 0)
		}
		vector := []float32{1, 0, 0, 0}
		meta := Metadata{Name: "Alice", RegisteredAt: time.Now().UTC()}
		first, err := newRegistry().Register(ctx, vector, meta)
		Expect(err).NotTo(HaveOccurred())
		again, err := newRegistry().Register(ctx, vector, meta)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(first))
		Expect(storage.entries).To(HaveLen(1))
		storage.entries = map[string][]byte{}
		restored, err := newRegistry().Register(ctx, vector, meta)
		Expect(err).NotTo(HaveOccurred())
		Expect(restored).To(Equal(first))
		Expect(storage.entries).To(HaveLen(1))
	})
	It("rejects zero and non-finite embeddings before writing", func(ctx SpecContext) {
		for _, v := range [][]float32{{0, 0}, {float32(math.NaN()), 1}, {float32(math.Inf(1)), 1}} {
			storage := &replayStore{entries: map[string][]byte{}}
			reg := NewStoreRegistry(func(context.Context, string) (grpc.Backend, error) { return storage, nil }, "faces", 0)
			_, err := reg.Register(ctx, v, Metadata{Name: "Alice"})
			Expect(err).To(HaveOccurred())
			Expect(storage.entries).To(BeEmpty())
		}
	})
})
