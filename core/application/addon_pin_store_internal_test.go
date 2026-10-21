// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"log/slog"
	"path/filepath"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func capture() *bytes.Buffer {
	var buf bytes.Buffer
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
	return &buf
}

var _ = Describe("pinStoreFor", func() {
	It("logs one warning for memory-only pins", func() {
		buf := capture()
		_, err := pinStoreFor(nil, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(bytes.Count(buf.Bytes(), []byte("memory only"))).To(Equal(1), "log: %q", buf.String())
	})

	It("does not warn with a pins file", func() {
		buf := capture()
		_, err := pinStoreFor(nil, filepath.Join(GinkgoT().TempDir(), "addon-pins.json"))
		Expect(err).ToNot(HaveOccurred())
		Expect(buf.Len()).To(BeZero(), "unexpected log: %q", buf.String())
	})
})
