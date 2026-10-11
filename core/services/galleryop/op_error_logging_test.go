package galleryop_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/xlog"
)

type failingModelManager struct{ err error }

func (m *failingModelManager) InstallModel(context.Context, *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], galleryop.ProgressCallback) error {
	return m.err
}

func (m *failingModelManager) DeleteModel(string) error { return m.err }

// A failed operation used to exist only in the job status. The UI's delete is
// fire-and-forget, so nothing read that status, and the server log had no
// trace of it either: a model that would not delete simply came back on the
// next rescan (#12562).
var _ = Describe("gallery operation failures", func() {
	It("are logged, not only recorded in the job status", func() {
		captured := &bytes.Buffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelError}), xlog.LogLevelError))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetModelManager(&failingModelManager{err: errors.New("failed to verify path /escape.bin: path is outside of trusted root")})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())

		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 "job-doomed",
			GalleryElementName: "doomed",
			Delete:             true,
		})

		Eventually(func() error {
			if st := svc.GetStatus("job-doomed"); st != nil && st.Processed {
				return st.Error
			}
			return nil
		}, "2s", "10ms").Should(MatchError(ContainSubstring("outside of trusted root")))
		Expect(captured.String()).To(And(
			ContainSubstring("Gallery operation failed"),
			ContainSubstring("op_id=job-doomed"),
			ContainSubstring("outside of trusted root"),
		))
	})
})
