package chv

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("watchConsole", func() {
	// It checks that the watcher reports a kernel panic in the console log
	// and stays quiet without one.
	It("reports a kernel panic in the console log and stays quiet without one", func() {
		dir := shortDir()
		path := filepath.Join(dir, "console.log")
		Expect(os.WriteFile(path, []byte("boot ok\n"), 0o600)).To(Succeed())
		done := make(chan struct{})
		panicked := make(chan struct{}, 1)
		go watchConsole(path, done, 10*time.Millisecond, 0, func() { panicked <- struct{}{} }, nil)
		Consistently(panicked, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive(), "reported a panic without one in the log")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		Expect(err).To(Succeed())
		_, err = f.WriteString("[  1.0] Kernel panic - not syncing: Attempted to kill init!\n")
		Expect(err).To(Succeed())
		f.Close()
		Eventually(panicked, 2*time.Second).Should(Receive(), "a panic in the log was not reported")
		close(done)
	})

	// It checks that a console log over the cap is reported once and that
	// trimConsole then keeps only the last bytes of it.
	It("reports a console log over the cap and trims it to the last bytes", func() {
		dir := shortDir()
		path := filepath.Join(dir, "console.log")
		body := append(bytes.Repeat([]byte("A"), 5000), []byte("TAIL-MARK")...)
		Expect(os.WriteFile(path, body[:1000], 0o600)).To(Succeed())
		done := make(chan struct{})
		defer close(done)
		flooded := make(chan struct{}, 4)
		go watchConsole(path, done, 10*time.Millisecond, 2048, func() { defer GinkgoRecover(); Fail("panic reported") }, func() { flooded <- struct{}{} })
		Consistently(flooded, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive(), "reported a flood under the cap")
		Expect(os.WriteFile(path, body, 0o600)).To(Succeed())
		Eventually(flooded, 2*time.Second).Should(Receive(), "a log over the cap was not reported")
		Expect(trimConsole(path, 100)).To(Succeed())
		got, err := os.ReadFile(path)
		Expect(err).To(Succeed())
		Expect(got).To(HaveLen(100))
		Expect(bytes.HasSuffix(got, []byte("TAIL-MARK"))).To(BeTrue(), "trimmed log: %q", got)
		// A log smaller than the keep size stays as it is.
		Expect(trimConsole(path, 1000)).To(Succeed())
		got2, _ := os.ReadFile(path)
		Expect(got2).To(Equal(got), "a small log changed")
	})
})
