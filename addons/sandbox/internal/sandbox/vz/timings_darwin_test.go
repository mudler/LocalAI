//go:build darwin && arm64

package vz

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// rssKB is the resident size in KiB of the processes that match the pattern
// (pgrep -f), or of this process when pattern is empty.
func rssKB(pattern string) int {
	pids := []string{strconv.Itoa(os.Getpid())}
	if pattern != "" {
		out, _ := exec.Command("pgrep", "-f", pattern).Output()
		pids = strings.Fields(string(out))
	}
	total := 0
	for _, p := range pids {
		out, err := exec.Command("ps", "-o", "rss=", "-p", p).Output()
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		total += n
	}
	return total
}

// The timing run is opt in: SANDBOX_TEST_TIMINGS is the number of runs. It
// prints one line for each run:
//
//	timing run=1 create=... first_command=... checkpoint_1=... checkpoint_1001=... refresh=... destroy=...
var _ = Describe("timings", func() {
	It("measures the life of a sandbox", func() {
		runs, _ := strconv.Atoi(os.Getenv("SANDBOX_TEST_TIMINGS"))
		if runs == 0 {
			Skip("set SANDBOX_TEST_TIMINGS to the number of runs")
		}
		b := testRealBackend()
		kernel := "image"
		if k := os.Getenv("SANDBOX_TEST_KERNEL"); k != "" {
			kernel = filepathBase(k)
		}
		ctx := context.Background()
		ms := func(d time.Duration) string { return fmt.Sprintf("%.0fms", float64(d.Microseconds())/1000) }
		for i := 1; i <= runs; i++ {
			sb, st := realFixtureQuiet(b)
			_ = st
			t := time.Now()
			sbx2, err := b.Create(ctx, sb.spec)
			Expect(err).To(Succeed())
			create := time.Since(t)
			s := sbx2.(*sbx)

			t = time.Now()
			sh(s, "true")
			first := time.Since(t)

			sh(s, "echo one > proj/one.txt")
			t = time.Now()
			_, _, err = s.Checkpoint(ctx, "one")
			Expect(err).To(Succeed())
			cp1 := time.Since(t)

			sh(s, `mkdir -p proj/many && i=0 && while [ $i -lt 1000 ]; do echo $i > proj/many/f$i.txt; i=$((i+1)); done`)
			// The checkpoint has two parts: the freeze and the read of the
			// upper image (this backend), and the commit to the workspace
			// store (shared by every backend, a SQLite store here).
			t = time.Now()
			var read time.Duration
			s.hooks.afterRead = func() { read = time.Since(t) }
			_, _, err = s.Checkpoint(ctx, "many")
			Expect(err).To(Succeed())
			cp1001 := time.Since(t)
			s.hooks.afterRead = nil

			t = time.Now()
			Expect(s.Refresh(ctx)).To(Succeed())
			refresh := time.Since(t)
			sh(s, "true")

			t = time.Now()
			Expect(s.Destroy(ctx)).To(Succeed())
			destroy := time.Since(t)
			GinkgoWriter.Printf("timing run=%d kernel=%s create=%s first_command=%s checkpoint_1=%s checkpoint_1001=%s (freeze_and_read=%s) refresh=%s destroy=%s\n",
				i, kernel, ms(create), ms(first), ms(cp1), ms(cp1001), ms(read), ms(refresh), ms(destroy))
			fmt.Printf("timing run=%d kernel=%s create=%s first_command=%s checkpoint_1=%s checkpoint_1001=%s (freeze_and_read=%s) refresh=%s destroy=%s\n",
				i, kernel, ms(create), ms(first), ms(cp1), ms(cp1001), ms(read), ms(refresh), ms(destroy))
		}
	})

	It("measures the memory of the host for idle VMs", func() {
		if os.Getenv("SANDBOX_TEST_TIMINGS") == "" {
			Skip("set SANDBOX_TEST_TIMINGS")
		}
		b := testRealBackend()
		ctx := context.Background()
		base := rssKB("")
		fmt.Printf("memory idle_vms=0 add_on_rss_kib=%d vm_service_rss_kib=%d\n", base, rssKB("Virtualization.VirtualMachine"))
		var made []sandbox.Sandbox
		for n := 1; n <= 4; n++ {
			fx, _ := realFixtureQuiet(b)
			sb, err := b.Create(ctx, fx.spec)
			Expect(err).To(Succeed())
			made = append(made, sb)
			time.Sleep(2 * time.Second)
			fmt.Printf("memory idle_vms=%d add_on_rss_kib=%d vm_service_rss_kib=%d\n", n, rssKB(""), rssKB("Virtualization.VirtualMachine"))
		}
		for _, sb := range made {
			_ = sb.Destroy(ctx)
		}
	})
})
