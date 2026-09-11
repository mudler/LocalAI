//go:build linux

package cli

import (
	"net"
	"os"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("selectSystemdListener", func() {
	It("keeps normal address binding when systemd passes no listener", func() {
		listener, err := selectSystemdListener(nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(listener).To(BeNil())
	})

	It("uses the single stream listener passed by systemd", func() {
		inherited, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(inherited.Close)

		listener, err := selectSystemdListener([]net.Listener{inherited})

		Expect(err).NotTo(HaveOccurred())
		Expect(listener).To(BeIdenticalTo(inherited))
	})

	It("rejects ambiguous activation with multiple stream listeners", func() {
		first, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(first.Close)
		second, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(second.Close)

		listener, err := selectSystemdListener([]net.Listener{first, second})

		Expect(err).To(MatchError(ContainSubstring("exactly one")))
		Expect(listener).To(BeNil())
	})
})

var _ = Describe("systemdActivatedListeners", func() {
	It("turns an inherited TCP file descriptor into a working listener", func() {
		original, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		file, err := original.(*net.TCPListener).File()
		Expect(err).NotTo(HaveOccurred())
		Expect(original.Close()).To(Succeed())

		listeners, err := listenersFromSystemdFDs(int(file.Fd()), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(listeners).To(HaveLen(1))
		DeferCleanup(listeners[0].Close)

		client, err := net.Dial("tcp", listeners[0].Addr().String())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.Close)
		server, err := listeners[0].Accept()
		Expect(err).NotTo(HaveOccurred())
		Expect(server.Close()).To(Succeed())
	})

	It("ignores descriptors intended for another process and clears the activation environment", func() {
		Expect(os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()+1))).To(Succeed())
		Expect(os.Setenv("LISTEN_FDS", "1")).To(Succeed())
		Expect(os.Setenv("LISTEN_FDNAMES", "localai-http")).To(Succeed())
		DeferCleanup(func() {
			_ = os.Unsetenv("LISTEN_PID")
			_ = os.Unsetenv("LISTEN_FDS")
			_ = os.Unsetenv("LISTEN_FDNAMES")
		})

		listeners, err := systemdActivatedListeners()

		Expect(err).NotTo(HaveOccurred())
		Expect(listeners).To(BeEmpty())
		Expect(os.Getenv("LISTEN_PID")).To(BeEmpty())
		Expect(os.Getenv("LISTEN_FDS")).To(BeEmpty())
		Expect(os.Getenv("LISTEN_FDNAMES")).To(BeEmpty())
	})

	It("binds normally when the environment leaks LISTEN_PID without LISTEN_FDS", func() {
		Expect(os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))).To(Succeed())
		Expect(os.Unsetenv("LISTEN_FDS")).To(Succeed())
		DeferCleanup(func() {
			_ = os.Unsetenv("LISTEN_PID")
		})

		listeners, err := systemdActivatedListeners()

		Expect(err).NotTo(HaveOccurred())
		Expect(listeners).To(BeEmpty())
		Expect(os.Getenv("LISTEN_PID")).To(BeEmpty())
	})

	It("binds normally when the environment leaks LISTEN_FDS without LISTEN_PID", func() {
		Expect(os.Unsetenv("LISTEN_PID")).To(Succeed())
		Expect(os.Setenv("LISTEN_FDS", "1")).To(Succeed())
		DeferCleanup(func() {
			_ = os.Unsetenv("LISTEN_FDS")
		})

		listeners, err := systemdActivatedListeners()

		Expect(err).NotTo(HaveOccurred())
		Expect(listeners).To(BeEmpty())
		Expect(os.Getenv("LISTEN_FDS")).To(BeEmpty())
	})

	It("reports malformed activation metadata instead of silently binding another socket", func() {
		Expect(os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))).To(Succeed())
		Expect(os.Setenv("LISTEN_FDS", "not-a-number")).To(Succeed())
		DeferCleanup(func() {
			_ = os.Unsetenv("LISTEN_PID")
			_ = os.Unsetenv("LISTEN_FDS")
		})

		listeners, err := systemdActivatedListeners()

		Expect(err).To(MatchError(ContainSubstring("LISTEN_FDS")))
		Expect(listeners).To(BeNil())
	})
})
