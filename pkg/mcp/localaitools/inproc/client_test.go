package inproc

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/system"
)

// Regression spec for the bug we fixed when channel sends were
// unconditional: with a never-read gallery channel and a pre-cancelled
// ctx, InstallModel must surface ctx.Err() instead of blocking forever.
// The same guarantee covers ImportModelURI, DeleteModel, InstallBackend,
// UpgradeBackend — they all share sendModelOp / sendBackendOp.
var _ = Describe("inproc.Client cancellation", func() {
	It("InstallModel returns context.Canceled when the gallery channel is never drained", func() {
		gs := &galleryop.GalleryService{
			// Unbuffered. Nothing reads from it in this spec, so a naive
			// send would block the goroutine indefinitely.
			ModelGalleryChannel: make(chan galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]),
		}
		c := &Client{
			AppConfig:   &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}}},
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}},
			Gallery:     gs,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel: the select must take the ctx.Done branch immediately.

		done := make(chan error, 1)
		go func() {
			_, err := c.InstallModel(ctx, localaitools.InstallModelRequest{ModelName: "x"})
			done <- err
		}()

		var err error
		Eventually(done, time.Second).Should(Receive(&err))
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got: %v", err)
	})
})
