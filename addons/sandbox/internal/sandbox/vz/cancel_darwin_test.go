//go:build darwin && arm64

package vz

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// vmServices counts the framework processes that run VMs. One exists for each
// VM that runs, so a VM that nobody tracks shows here.
func vmServices() int {
	out, _ := exec.Command("pgrep", "-f", "com.apple.Virtualization.VirtualMachine").Output()
	return len(strings.Fields(string(out)))
}

// openFiles lists the processes that hold path open, or nothing.
func openFiles(path string) string {
	out, _ := exec.Command("lsof", "-t", path).Output()
	return strings.TrimSpace(string(out))
}

// bareMachine makes a machine over the guest image of the tests, with its
// own upper image, and returns it with the path of that image.
func bareMachine(b *Backend) (machine, string) {
	GinkgoHelper()
	img, err := b.image(context.Background(), "")
	Expect(err).To(Succeed())
	dir := shortDir()
	upper := filepath.Join(dir, "upper.ext4")
	Expect(ext4mk.FormatFile(upper, ext4mk.Options{Size: 64 << 20, RootUID: 1000, RootGID: 1000})).To(Succeed())
	Expect(os.Mkdir(filepath.Join(dir, "lower"), 0o700)).To(Succeed())
	m, err := b.mk(vmm.Config{
		Kernel: img.Kernel, Cmdline: cmdline(state{}), Root: img.Root, Upper: upper,
		Lower: filepath.Join(dir, "lower"), Console: filepath.Join(dir, "console.log"), CPUs: 1, MemoryBytes: 512 << 20,
	})
	Expect(err).To(Succeed())
	return m, upper
}

// A VM that is still starting cannot be stopped. A Start that the context
// cancels must wait for the start, then stop the VM: otherwise the VM runs
// on without an owner and keeps its disk images open.
var _ = Describe("a start that the context cancels", func() {
	for _, delay := range []time.Duration{0, 10 * time.Millisecond, 30 * time.Millisecond, 60 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond} {
		It(fmt.Sprintf("leaves no VM running after %v", delay), func() {
			b := testRealBackend()
			before := vmServices()
			m, upper := bareMachine(b)
			ctx, cancel := context.WithTimeout(context.Background(), delay)
			defer cancel()
			err := m.Start(ctx)
			if err != nil {
				Expect(err).To(MatchError(context.DeadlineExceeded))
			}
			// Start won the race at the longer delays: the VM runs, and Stop
			// must still end it.
			m.Stop()
			Expect(m.Done()).To(BeClosed(), "Stop returned and the VM is not marked stopped")
			// The service of a stopped VM goes within a second or two. A VM
			// that nothing stopped runs until its guest ends it by itself.
			Eventually(vmServices, 5*time.Second, 50*time.Millisecond).Should(BeNumerically("<=", before), "a VM service is left after Stop")
			Expect(openFiles(upper)).To(BeEmpty(), "a process still holds the upper image")
			Expect(os.Remove(upper)).To(Succeed())
		})
	}

	for _, started := range []bool{true, false} {
		It(fmt.Sprintf("lets a Dial on a stopped machine give up without a goroutine left (started: %v)", started), func() {
			dialStopped(started)
		})
	}
})

func dialStopped(started bool) {
	{
		b := testRealBackend()
		m, _ := bareMachine(b)
		if started {
			Expect(m.Start(context.Background())).To(Succeed())
		}
		m.Stop()
		Expect(m.Done()).To(BeClosed())
		runtime.GC()
		base := runtime.NumGoroutine()
		for range 20 {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, err := m.Dial(ctx, guestagent.AgentPort)
			cancel()
			Expect(err).To(HaveOccurred())
		}
		Eventually(runtime.NumGoroutine, 5*time.Second, 100*time.Millisecond).Should(BeNumerically("<=", base+2), "Dial left goroutines")
	}
}
