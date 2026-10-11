// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpclib "google.golang.org/grpc"
	"time"
)

type diagnosticMetadataBackend struct {
	grpc.Backend
	calls int
	reply *pb.ModelMetadataResponse
	err   error
}

func (b *diagnosticMetadataBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...grpclib.CallOption) (*pb.ModelMetadataResponse, error) {
	b.calls++
	time.Sleep(time.Millisecond)
	return b.reply, b.err
}

var _ = Describe("Metadata dispatch", func() {
	It("separates decisions and results without caching empty results or claiming RPC events", func() {
		for _, tc := range []struct {
			reply   *pb.ModelMetadataResponse
			err     error
			phase   diagnostics.Phase
			outcome diagnostics.Outcome
		}{
			{nil, nil, diagnostics.PhaseMetadataEmpty, diagnostics.OutcomeOK},
			{&pb.ModelMetadataResponse{}, nil, diagnostics.PhaseMetadataEmpty, diagnostics.OutcomeOK},
			{&pb.ModelMetadataResponse{MediaMarker: "sentinel"}, nil, diagnostics.PhaseMetadataNonempty, diagnostics.OutcomeOK},
			{nil, errors.New("private error"), "", diagnostics.OutcomeError},
			{nil, context.Canceled, "", diagnostics.OutcomeCanceled},
		} {
			var events []diagnostics.Event
			ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
			b := &diagnosticMetadataBackend{reply: tc.reply, err: tc.err}
			cfg := &ModelConfig{}
			for range 2 {
				DetectThinkingSupportFromBackend(ctx, cfg, b, &pb.ModelOptions{})
			}
			diagnostics.FlushDispatch(ctx)
			Expect(b.calls).To(Equal(2))
			var ends, results int
			for _, e := range events {
				Expect(e.Phase).NotTo(Equal(diagnostics.PhaseMetadataRPC))
				if e.Phase == diagnostics.PhaseMetadataDecision && e.State == diagnostics.StateEnd {
					ends++
					Expect(e.Outcome).To(Equal(tc.outcome))
					Expect(e.Elapsed).To(BeNumerically(">=", time.Millisecond))
				}
				if e.Phase == tc.phase {
					results++
				}
			}
			Expect(ends).To(Equal(2))
			if tc.phase != "" {
				Expect(results).To(Equal(2))
			}
		}
	})
})
