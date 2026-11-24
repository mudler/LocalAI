// SPDX-License-Identifier: MIT
package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/stdiomux"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The stdio channel mode (an add-on in WSL 2)", func() {
	// owner runs the owner's side of a session over an in-memory connection
	// and returns it with the add-on's side.
	type opened struct {
		c   *stdioChannel
		err error
	}
	start := func() (*stdioOwner, chan opened) {
		ownerEnd, addonEnd := net.Pipe()
		done := make(chan opened, 1)
		go func() {
			c, err := openStdio(addonEnd, 4242)
			done <- opened{c, err}
		}()
		session, err := stdiomux.Client(ownerEnd)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = session.Close() })
		first, err := session.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		return &stdioOwner{session: session, first: first, lines: bufio.NewReader(first)}, done
	}

	It("says its process ID, then hands the add-on the startup object and the end of the stream", func() {
		o, done := start()
		Expect(o.lines.ReadString('\n')).To(Equal("4242\n"))
		_, err := io.WriteString(o.first, `{"credential":"c","dataDir":"/data"}`+"\n")
		Expect(err).NotTo(HaveOccurred())
		var r opened
		Eventually(done, 2*time.Second).Should(Receive(&r))
		Expect(r.err).NotTo(HaveOccurred())
		s, rest, err := ReadStartup(r.c.startup)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.DataDir).To(Equal("/data"))
		Expect(s.HostEndpoint).To(BeEmpty(), "no host endpoint, no relay")
		Expect(r.c.relay).To(BeNil())
		ended := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, rest); close(ended) }()
		Consistently(ended, 100*time.Millisecond).ShouldNot(BeClosed())
		Expect(o.first.Close()).To(Succeed())
		Eventually(ended, 2*time.Second).Should(BeClosed(), "the close of the stream is the stop signal")
	})

	It("ends the startup channel when the whole session ends", func() {
		o, done := start()
		Expect(o.lines.ReadString('\n')).To(Equal("4242\n"))
		_, _ = io.WriteString(o.first, `{"dataDir":"/data"}`+"\n")
		var r opened
		Eventually(done, 2*time.Second).Should(Receive(&r))
		Expect(r.err).NotTo(HaveOccurred())
		_, rest, err := ReadStartup(r.c.startup)
		Expect(err).NotTo(HaveOccurred())
		ended := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, rest); close(ended) }()
		Expect(o.session.Close()).To(Succeed())
		Eventually(ended, 2*time.Second).Should(BeClosed())
	})

	It("serves the protocol on the streams the owner opens", func() {
		o, done := start()
		Expect(o.lines.ReadString('\n')).To(Equal("4242\n"))
		_, _ = io.WriteString(o.first, `{"dataDir":"/data"}`+"\n")
		var r opened
		Eventually(done, 2*time.Second).Should(Receive(&r))
		Expect(r.err).NotTo(HaveOccurred())
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pong") }), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(r.c.listener()) }()
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return o.session.Open(ctx) }}}
		resp, err := client.Get("http://addon/health")
		Expect(err).NotTo(HaveOccurred())
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(string(b)).To(Equal("pong"))
	})

	It("carries the host endpoint over the session through a loopback relay", func() {
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "host saw "+r.URL.Path)
		}))
		DeferCleanup(host.Close)
		o, done := start()
		// The owner splices each stream the add-on opens to its endpoint.
		go func() {
			for {
				s, err := o.session.Accept()
				if err != nil {
					return
				}
				go func() {
					c, err := net.Dial("tcp", strings.TrimPrefix(host.URL, "http://"))
					if err != nil {
						_ = s.Close()
						return
					}
					stdiomux.Splice(s, c)
				}()
			}
		}()
		Expect(o.lines.ReadString('\n')).To(Equal("4242\n"))
		b, _ := json.Marshal(map[string]string{"dataDir": "/data", "hostEndpoint": "http://127.0.0.1:1", "hostCredential": "h"})
		_, _ = o.first.Write(append(b, '\n'))
		var r opened
		Eventually(done, 2*time.Second).Should(Receive(&r))
		Expect(r.err).NotTo(HaveOccurred())
		s, _, err := ReadStartup(r.c.startup)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.HostCredential).To(Equal("h"))
		Expect(s.HostEndpoint).To(HavePrefix("http://127.0.0.1:"))
		Expect(s.HostEndpoint).NotTo(Equal("http://127.0.0.1:1"))
		resp, err := http.Get(s.HostEndpoint + "/op")
		Expect(err).NotTo(HaveOccurred())
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(string(body)).To(Equal("host saw /op"))
		Expect(o.session.Close()).To(Succeed())
		Eventually(func() error {
			c, err := net.DialTimeout("tcp", strings.TrimPrefix(s.HostEndpoint, "http://"), 100*time.Millisecond)
			if err == nil {
				_ = c.Close()
			}
			return err
		}, 2*time.Second).Should(HaveOccurred(), "the relay ends with the session")
	})

	It("refuses a startup stream that carries no startup object", func() {
		o, done := start()
		Expect(o.lines.ReadString('\n')).To(Equal("4242\n"))
		_, _ = io.WriteString(o.first, "not json\n")
		var r opened
		Eventually(done, 2*time.Second).Should(Receive(&r))
		Expect(r.err).To(MatchError(ContainSubstring("startup object")))
	})

	It("gives up when the owner opens no startup stream", func() {
		old := stdioAcceptWait
		stdioAcceptWait = 50 * time.Millisecond
		DeferCleanup(func() { stdioAcceptWait = old })
		ownerEnd, addonEnd := net.Pipe()
		session, err := stdiomux.Client(ownerEnd)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = session.Close() })
		_, err = openStdio(addonEnd, 1)
		Expect(err).To(MatchError(ContainSubstring("no startup stream")))
	})
})

type stdioOwner struct {
	session interface {
		Open(context.Context) (net.Conn, error)
		Accept() (net.Conn, error)
		Close() error
	}
	first net.Conn
	lines *bufio.Reader
}
