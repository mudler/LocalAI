// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"errors"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load options finalizer", func() {
	It("runs on a copy right before a real load and leaves the shared options alone", func() {
		ml := NewModelLoader(&system.SystemState{})
		base := &pb.ModelOptions{Proxy: &pb.ProxyOptions{UpstreamUrl: "https://example.com"}}
		var seen *pb.ModelOptions
		ml.SetModelRouter(func(_ context.Context, _, _, _, _, _ string, options *pb.ModelOptions, _ bool) (*Model, error) {
			seen = options
			return NewModel("id", "x", nil), nil
		})
		opts := NewOptions(WithLoadGRPCLoadModelOpts(base),
			WithLoadGRPCOptionsFinalizer(func(_ context.Context, o *pb.ModelOptions) error {
				o.Proxy.ApiKey = "fake-key"
				return nil
			}))
		_, err := ml.grpcModel("b", opts)("id", "n", "f")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen.Proxy.ApiKey).To(Equal("fake-key"))
		Expect(base.Proxy.ApiKey).To(BeEmpty())
	})

	It("fails the load with the finalizer error", func() {
		ml := NewModelLoader(&system.SystemState{})
		called := false
		ml.SetModelRouter(func(context.Context, string, string, string, string, string, *pb.ModelOptions, bool) (*Model, error) {
			called = true
			return nil, nil
		})
		cause := errors.New("no key")
		opts := NewOptions(WithLoadGRPCOptionsFinalizer(func(context.Context, *pb.ModelOptions) error { return cause }))
		_, err := ml.grpcModel("b", opts)("id", "n", "f")
		Expect(err).To(MatchError(cause))
		Expect(called).To(BeFalse())
	})
})
