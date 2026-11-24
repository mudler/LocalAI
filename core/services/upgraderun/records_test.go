package upgraderun_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

var _ = Describe("A stored run with a corrupt JSON column", func() {
	It("reads the rest of the run and logs the broken row once, without its content", func() {
		captured := &bytes.Buffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelWarn}), xlog.LogLevelWarn))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })

		h := newHarness(harnessOpts{})
		done := time.Now()
		Expect(h.store.Create(&distributed.UpgradeRunRecord{ID: "run-corrupt", Source: "manual", State: "finished", FinishedAt: &done},
			[]distributed.UpgradeRunItemRecord{
				{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "failed", ErrorKey: "start", Error: "did not start",
					Nodes: `[{"id":"node-secret-1"`, Extra: `{"pin": "digest-secret"`},
				{Key: "gallery:b", Kind: "gallery", Name: "b", Phase: "updated",
					Nodes: `[{"id":"n2","name":"gpu-b","phase":"updated","progress":100}]`},
			})).To(Succeed())

		run := h.get("run-corrupt")
		Expect(run.Items).To(HaveLen(2))
		a := run.Item("gallery:a")
		Expect(a.Phase).To(Equal(PhaseFailed))
		Expect(a.ErrorKey).To(Equal("start"))
		Expect(a.Error).To(Equal("did not start"))
		Expect(a.Nodes).To(BeEmpty())
		Expect(a.Extra).To(BeEmpty())
		Expect(run.Item("gallery:b").Nodes).To(HaveLen(1), "a broken row does not cost the other rows")

		logged := captured.String()
		Expect(logged).To(And(
			ContainSubstring("run-corrupt"),
			ContainSubstring("gallery:a"),
			ContainSubstring("nodes"),
			ContainSubstring("extra"),
		), "the broken row and columns are named")
		Expect(logged).NotTo(Or(ContainSubstring("node-secret-1"), ContainSubstring("digest-secret")), "the content of the row is not logged")
		Expect(logged).NotTo(ContainSubstring("gallery:b"))

		// Reading it again, alone or in a list, logs nothing new.
		h.get("run-corrupt")
		_, err := h.svc.List(context.Background(), ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(captured.String()).To(Equal(logged))
		Expect(strings.Count(logged, "gallery:a")).To(BeNumerically(">", 0))
	})
})
