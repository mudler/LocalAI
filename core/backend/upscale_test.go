// SPDX-License-Identifier: MIT

package backend_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"
)

type upscaleBackend struct {
	grpcPkg.Backend
	result  *pb.Result
	err     error
	request *pb.UpscaleImageRequest
}

func (*upscaleBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*upscaleBackend) IsBusy() bool                              { return false }
func (b *upscaleBackend) UpscaleImage(_ context.Context, in *pb.UpscaleImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.request = in
	return b.result, b.err
}

func TestImageUpscaleResult(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, tc := range []struct {
		name   string
		result *pb.Result
		err    error
		want   string
	}{
		{name: "nil", want: "nil"},
		{name: "unsuccessful", result: &pb.Result{}, want: "failed"},
		{name: "message", result: &pb.Result{Message: "native mismatch"}, want: "native mismatch"},
		{name: "transport", err: transportErr, want: "transport failed"},
		{name: "success", result: &pb.Result{Success: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &upscaleBackend{result: tc.result, err: tc.err}
			loader := model.NewModelLoader(&system.SystemState{})
			loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
				return model.NewModelWithClient(id, "test://upscale", fake), nil
			})
			cfg := config.ModelConfig{Name: "upscale", Backend: "stub"}
			cfg.SetDefaults()
			fn, err := backend.ImageUpscale(context.Background(), "source", "destination", 3, loader, cfg, config.NewApplicationConfig(config.WithSystemState(&system.SystemState{})))
			if err != nil {
				t.Fatal(err)
			}
			err = fn()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost transport error: %v", err)
			}
			if fake.request == nil || fake.request.Scale != 3 || fake.request.Src != "source" || fake.request.Dst != "destination" {
				t.Fatalf("unexpected request: %v", fake.request)
			}
		})
	}
}
