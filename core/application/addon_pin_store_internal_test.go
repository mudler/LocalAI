// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const warningCaseEnv = "LOCALAI_WARNING_TEST_CASE"
const warningDirEnv = "LOCALAI_WARNING_TEST_DIR"

// Run only the helper, not the Ginkgo suite: other specs leave background log
// producers running. Replacing xlog's global logger alongside them is a race.
func runWarningCase(test, id string) {
	executable, err := os.Executable()
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+test+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), warningCaseEnv+"="+id, warningDirEnv+"="+GinkgoT().TempDir())
	output := &warningOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred(), "warning case %s (%v):\n%s", id, ctx.Err(), output.String())
	Expect(output.String()).To(ContainSubstring("warning case passed: "+id), "child must execute its assertions")
}

// Keep failure diagnostics bounded even if a child unexpectedly logs forever.
type warningOutput struct{ bytes.Buffer }

func (b *warningOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 64*1024 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func TestPinStoreWarningProcess(t *testing.T) {
	id := os.Getenv(warningCaseEnv)
	if id == "" {
		return
	}
	RegisterTestingT(t)
	var buf bytes.Buffer
	// This process starts no background application and never restores the global.
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
	switch id {
	case "memory-pins":
		_, err := pinStoreFor(nil, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Count(buf.Bytes(), []byte("memory only"))).To(Equal(1), "log: %q", buf.String())
	case "file-pins":
		_, err := pinStoreFor(nil, filepath.Join(os.Getenv(warningDirEnv), "addon-pins.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(buf.Len()).To(BeZero(), "unexpected log: %q", buf.String())
	default:
		Expect(id).To(BeEmpty(), "unknown pin warning case")
	}
	fmt.Println("warning case passed: " + id)
}

var _ = Describe("pinStoreFor", func() {
	It("logs one warning for memory-only pins", func() {
		runWarningCase("TestPinStoreWarningProcess", "memory-pins")
	})
	It("does not warn with a pins file", func() {
		runWarningCase("TestPinStoreWarningProcess", "file-pins")
	})
})
