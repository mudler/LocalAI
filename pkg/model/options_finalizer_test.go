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
	It("does not run when a router takes the load, so a routed request costs no lookup", func() {
		ml := NewModelLoader(&system.SystemState{})
		base := &pb.ModelOptions{Proxy: &pb.ProxyOptions{UpstreamUrl: "https://example.com"}}
		var seen *pb.ModelOptions
		ml.SetModelRouter(func(_ context.Context, _, _, _, _, _ string, options *pb.ModelOptions, _ bool) (*Model, error) {
			seen = options
			return NewModel("id", "x", nil), nil
		})
		calls := 0
		opts := NewOptions(WithLoadGRPCLoadModelOpts(base),
			WithLoadGRPCOptionsFinalizer(func(_ context.Context, o *pb.ModelOptions) error {
				calls++
				o.Proxy.ApiKey = "fake-key"
				return nil
			}))
		for range 3 {
			_, err := ml.grpcModel("b", opts)("id", "n", "f")
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(calls).To(BeZero())
		Expect(seen.Proxy.ApiKey).To(BeEmpty())
		Expect(base.Proxy.ApiKey).To(BeEmpty())
	})

	It("runs on a copy for a local load and leaves the shared options alone", func() {
		ml := NewModelLoader(&system.SystemState{})
		base := &pb.ModelOptions{Proxy: &pb.ProxyOptions{UpstreamUrl: "https://example.com"}}
		var seen string
		opts := NewOptions(WithLoadGRPCLoadModelOpts(base),
			WithLoadGRPCOptionsFinalizer(func(_ context.Context, o *pb.ModelOptions) error {
				o.Proxy.ApiKey = "fake-key"
				seen = o.Proxy.ApiKey
				return nil
			}))
		got, err := opts.finalized()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.gRPCOptions.Proxy.ApiKey).To(Equal("fake-key"))
		Expect(seen).To(Equal("fake-key"))
		Expect(base.Proxy.ApiKey).To(BeEmpty())
		// A local load of an unknown backend still fails, after the finalizer.
		_, err = ml.grpcModel("no-such-backend", opts)("id", "n", "f")
		Expect(err).To(MatchError(ErrBackendNotFound))
	})

	It("fails a local load with the finalizer error before anything starts", func() {
		ml := NewModelLoader(&system.SystemState{})
		cause := errors.New("no key")
		opts := NewOptions(WithLoadGRPCLoadModelOpts(&pb.ModelOptions{}),
			WithLoadGRPCOptionsFinalizer(func(context.Context, *pb.ModelOptions) error { return cause }))
		_, err := ml.grpcModel("b", opts)("id", "n", "f")
		Expect(err).To(MatchError(cause))
	})
})
