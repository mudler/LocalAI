package inproc

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/system"
)

// TestInstallModel_RespectsCancelledContext exercises the bug we fixed
// when channel sends were unconditional: with a never-read gallery channel
// and a pre-cancelled ctx, InstallModel must surface ctx.Err() instead of
// blocking forever. The same guarantee covers ImportModelURI, DeleteModel,
// InstallBackend, UpgradeBackend — they all use sendModelOp / sendBackendOp.
func TestInstallModel_RespectsCancelledContext(t *testing.T) {
	gs := &galleryop.GalleryService{
		// Unbuffered. Nothing reads from it in this test, so a naive send
		// would block the goroutine indefinitely.
		ModelGalleryChannel: make(chan galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]),
	}
	c := &Client{
		AppConfig:   &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: t.TempDir()}}},
		SystemState: &system.SystemState{Model: system.Model{ModelsPath: t.TempDir()}},
		Gallery:     gs,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the select must take the ctx.Done branch immediately.

	done := make(chan error, 1)
	go func() {
		_, err := c.InstallModel(ctx, localaitools.InstallModelRequest{ModelName: "x"})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("InstallModel err = %v, want context.Canceled", err)
		}
	case <-context.Background().Done():
		// unreachable
	}
}
