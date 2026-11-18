//go:build linux || darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Windows passes the channels as named handles and the child reports its
// endpoint. These specs run that way on POSIX systems, where the handles are
// descriptor numbers, so the wire logic is checked on every platform.
var _ = Describe("Channels passed as named handles", func() {
	reported := func() *Supervisor {
		if os.Getenv("ADDON_RUNTIME_WSL") == "1" {
			Skip("the named handles are not used for an add-on in WSL")
		}
		s := supervisor()
		s.reportEndpoint = true
		return s
	}
	It("starts an add-on that reports its endpoint, serves it and stops it", func() {
		s := reported()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.registration.Endpoint).To(HavePrefix("http://127.0.0.1:"))
		Expect(i.process.cmd.ExtraFiles).To(HaveLen(2))
		Expect(i.process.cmd.Env).To(ContainElements(HavePrefix(contract.StartupHandleEnv+"=3"), HavePrefix(contract.EndpointHandleEnv+"=4")))
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(r[0].Endpoint).To(Equal(i.process.registration.Endpoint))
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})
	It("gives the child only the fixed environment and the two handle names", func() {
		GinkgoT().Setenv("LOCALAI_RUNTIME_TEST_SECRET", "must-not-leak")
		s := reported()
		i, e := s.Start(context.Background(), fixture("env-check", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Env).To(HaveLen(4))
		Expect(i.process.parent.Close()).To(Succeed())
		Eventually(i.process.done, 2*time.Second).Should(BeClosed())
	})
	It("refuses a child that reports an address that is not loopback", func() {
		s := reported()
		_, e := s.Start(context.Background(), fixture("report-remote", service), 1)
		Expect(e).To(MatchError(ErrRuntime))
		r, _ := s.Registry.Snapshot(context.Background())
		Expect(r).To(BeEmpty())
	})
	It("stops a child that never reports within the ready timeout", func() {
		s := reported()
		start := time.Now()
		_, e := s.Start(context.Background(), fixture("report-silent", service), 1)
		Expect(e).To(MatchError(ErrRuntime))
		Expect(time.Since(start)).To(BeNumerically("<", startBudget+1700*time.Millisecond))
	})
	It("restarts a crashed add-on in the same way", func() {
		s := reported()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() string {
			r, _ := s.Registry.Snapshot(context.Background())
			if len(r) != 1 {
				return ""
			}
			return r[0].Incarnation
		}, 3*time.Second).ShouldNot(Or(BeEmpty(), Equal(i.Incarnation)))
		Expect(s.Close(context.Background())).To(Succeed())
	})
})

var _ = Describe("The endpoint report", func() {
	DescribeTable("is accepted only as one 127.0.0.1 line",
		func(line string, want string) {
			got, e := parseEndpointReport([]byte(line))
			if want == "" {
				Expect(e).To(MatchError(ErrRuntime))
				return
			}
			Expect(e).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("loopback", "127.0.0.1:49152\n", "http://127.0.0.1:49152"),
		Entry("no newline", "127.0.0.1:49152", ""),
		Entry("two lines", "127.0.0.1:1\n127.0.0.1:2\n", ""),
		Entry("other host", "192.0.2.1:80\n", ""),
		Entry("other loopback", "127.0.0.2:80\n", ""),
		Entry("IPv6 loopback", "[::1]:80\n", ""),
		Entry("name", "localhost:80\n", ""),
		Entry("port zero", "127.0.0.1:0\n", ""),
		Entry("port too large", "127.0.0.1:65536\n", ""),
		Entry("leading zero", "127.0.0.1:080\n", ""),
		Entry("sign", "127.0.0.1:+80\n", ""),
		Entry("empty", "", ""),
		Entry("too long", "127.0.0.1:80"+strings.Repeat(" ", contract.MaxEndpointReport)+"\n", ""),
	)
	It("is not waited for when the owner bound the listener", func() {
		cmd := exec.Command("true")
		c, e := openDescriptorChannels(cmd)
		Expect(e).NotTo(HaveOccurred())
		defer func() { c.release(); _ = c.parent.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, e := c.awaitEndpoint(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(HavePrefix("http://127.0.0.1:"))
		Expect(cmd.ExtraFiles).To(HaveLen(2))
		Expect(cmd.Env).To(BeEmpty())
	})
	It("writes the startup object through a pipe with a deadline", func() {
		cmd := exec.Command("true")
		c, e := openReportedChannels(cmd)
		Expect(e).NotTo(HaveOccurred())
		defer func() { c.release(); _ = c.parent.Close() }()
		go func() {
			_ = writeStartup(context.Background(), c.parent, time.Now().Add(time.Second), Startup{DataDir: "/data"})
			_ = c.parent.Close()
		}()
		var buf bytes.Buffer
		_, e = buf.ReadFrom(c.child[0])
		Expect(e).NotTo(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring(`"dataDir":"/data"`))
	})
})
