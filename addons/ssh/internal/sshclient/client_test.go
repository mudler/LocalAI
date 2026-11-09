// SPDX-License-Identifier: MIT
package sshclient_test

import (
	"context"
	"io"
	"net"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/ssh/internal/credential"
	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/addons/ssh/sshtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ctx = context.Background()

func targetOf(s *sshtest.Server, user string) sshclient.Target {
	t := sshclient.Target{User: user, Host: s.Host(), Port: s.Port()}
	t.Pin = sshclient.PinHex(s.HostKey())
	return t
}

func mustCred(secret string) *credential.Credential {
	c, err := credential.Parse(secret)
	Expect(err).NotTo(HaveOccurred())
	return c
}

var _ = Describe("Connect and Run", func() {
	It("logs in with a private key", func() {
		pub, pemKey := sshtest.NewClientKey()
		srv := sshtest.New(sshtest.Options{User: "deploy", Key: pub})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred(pemKey))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		res, err := conn.Run(ctx, "echo hi", 5*time.Second, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ExitCode).To(Equal(0))
		Expect(res.Stdout).To(Equal("ran: echo hi\n"))
	})

	It("logs in with a PKCS#8 key", func() {
		pub, pemKey := sshtest.NewClientKeyPKCS8()
		srv := sshtest.New(sshtest.Options{User: "deploy", Key: pub})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred(pemKey))
		Expect(err).NotTo(HaveOccurred())
		conn.Close()
	})

	It("logs in with a password", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "p4ss:word"})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:p4ss:word"))
		Expect(err).NotTo(HaveOccurred())
		conn.Close()
	})

	It("says the login was refused, apart from the other failures", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "right-password"})
		defer srv.Close()
		_, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:wrong-password"))
		Expect(err).To(MatchError(sshclient.ErrAuth))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-password"))
		_, err = sshclient.Connect(ctx, targetOf(srv, "someone"), mustCred("someone:right-password"))
		Expect(err).To(MatchError(sshclient.ErrAuth))
	})

	It("says the host cannot be reached when nothing listens", func() {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		_ = ln.Close()
		t := sshclient.Target{User: "deploy", Host: "127.0.0.1", Port: port, Pin: pin}
		_, err := sshclient.Connect(ctx, t, mustCred("deploy:secret-pass"))
		Expect(err).To(MatchError(sshclient.ErrUnreachable))
	})

	It("says the handshake failed when the port does not speak SSH", func() {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer func() { _ = ln.Close() }()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
				_ = c.Close()
			}
		}()
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		t := sshclient.Target{User: "deploy", Host: "127.0.0.1", Port: port, Pin: pin}
		_, err := sshclient.Connect(ctx, t, mustCred("deploy:secret-pass"))
		Expect(err).To(MatchError(sshclient.ErrHandshake))
	})

	It("refuses to connect without a pin", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass"})
		defer srv.Close()
		t := targetOf(srv, "deploy")
		t.Pin = ""
		_, err := sshclient.Connect(ctx, t, mustCred("deploy:secret-pass"))
		Expect(err).To(MatchError(sshclient.ErrNoPin))
		Expect(srv.Logins()).To(Equal(0))
	})

	It("refuses a changed host key before it sends any credential", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass"})
		defer srv.Close()
		t := targetOf(srv, "deploy")
		srv.RotateHostKey()
		_, err := sshclient.Connect(ctx, t, mustCred("deploy:secret-pass"))
		var changed *sshclient.HostKeyChangedError
		Expect(err).To(HaveOccurred())
		Expect(errorsAs(err, &changed)).To(BeTrue())
		Expect(changed.Pinned).To(Equal(sshclient.FingerprintOfPin(t.Pin)))
		Expect(changed.Presented).To(Equal(sshclient.FingerprintOfPin(sshclient.PinHex(srv.HostKey()))))
		Expect(err.Error()).To(ContainSubstring("host key changed"))
		Expect(err.Error()).To(ContainSubstring("Only the person can trust a new key"))
		// The server never saw a login attempt: no password was sent.
		Expect(srv.Refused()).To(Equal(0))
		Expect(srv.Logins()).To(Equal(0))
	})

	It("returns the exit code of a failing command and its error stream", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass", Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, "out")
			_, _ = io.WriteString(s.Stderr, "boom")
			return 7
		}})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:secret-pass"))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		res, err := conn.Run(ctx, "false", 5*time.Second, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ExitCode).To(Equal(7))
		Expect(res.Stdout).To(Equal("out"))
		Expect(res.Stderr).To(Equal("boom"))
		Expect(res.TimedOut).To(BeFalse())
	})

	It("sends no terminal, environment or shell request, and closes the input", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass", Handler: func(cmd string, s sshtest.Streams) int { return 0 }})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:secret-pass"))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		_, err = conn.Run(ctx, "true", 5*time.Second, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Requests()).To(Equal([]string{"exec"}))
	})

	It("cuts each stream at the cap, flags it, and keeps valid text", func() {
		big := strings.Repeat("é", sshclient.MaxOutput) // two bytes each
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass", Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, big)
			_, _ = io.WriteString(s.Stderr, "small")
			return 0
		}})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:secret-pass"))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		res, err := conn.Run(ctx, "flood", 10*time.Second, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(res.Stdout)).To(BeNumerically("<=", sshclient.MaxOutput))
		Expect(len(res.Stdout)).To(BeNumerically(">", sshclient.MaxOutput-4))
		Expect(res.StdoutTruncated).To(BeTrue())
		Expect(strings.ToValidUTF8(res.Stdout, "?")).To(Equal(res.Stdout))
		Expect(res.Stderr).To(Equal("small"))
		Expect(res.StderrTruncated).To(BeFalse())
	})

	It("scrubs a secret that crosses the cut", func() {
		secret := "S3cretPassword!"
		filler := strings.Repeat("x", sshclient.MaxOutput-5)
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: secret, Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, filler+secret+"tail")
			return 0
		}})
		defer srv.Close()
		c := mustCred("deploy:" + secret)
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), c)
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		res, err := conn.Run(ctx, "x", 10*time.Second, nil, c.Scrub)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Stdout).NotTo(ContainSubstring("S3cret"))
		Expect(res.Stdout).To(HaveSuffix("[redacted]"[:5]))
		Expect(res.StdoutTruncated).To(BeTrue())
	})

	It("stops a command at the timeout and returns what it wrote", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass", Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, "started\n")
			select {
			case <-s.Done:
			case <-time.After(20 * time.Second):
			}
			return 0
		}})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:secret-pass"))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		begin := time.Now()
		res, err := conn.Run(ctx, "sleep 100", 300*time.Millisecond, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.TimedOut).To(BeTrue())
		Expect(res.ExitCode).To(Equal(-1))
		Expect(res.Stdout).To(Equal("started\n"))
		Expect(time.Since(begin)).To(BeNumerically("<", 5*time.Second))
	})

	It("does not send the command when onStart fails", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass"})
		defer srv.Close()
		conn, err := sshclient.Connect(ctx, targetOf(srv, "deploy"), mustCred("deploy:secret-pass"))
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		_, err = conn.Run(ctx, "never", 5*time.Second, func() error { return io.ErrClosedPipe }, nil)
		Expect(err).To(MatchError(io.ErrClosedPipe))
		Expect(srv.Commands()).To(BeEmpty())
	})
})

var _ = Describe("ScanHostKey", func() {
	It("reads the key and sends no credential", func() {
		srv := sshtest.New(sshtest.Options{User: "deploy", Password: "secret-pass"})
		defer srv.Close()
		k, err := sshclient.ScanHostKey(ctx, sshclient.Target{User: "deploy", Host: srv.Host(), Port: srv.Port()})
		Expect(err).NotTo(HaveOccurred())
		Expect(k.Type).To(Equal("ssh-ed25519"))
		Expect(k.Pin).To(Equal(sshclient.PinHex(srv.HostKey())))
		Expect(k.Fingerprint).To(Equal(sshclient.FingerprintOfPin(k.Pin)))
		Expect(srv.Logins()).To(Equal(0))
		Expect(srv.Refused()).To(Equal(0))
	})

	It("says the host is unreachable when nothing listens", func() {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		_ = ln.Close()
		_, err := sshclient.ScanHostKey(ctx, sshclient.Target{User: "deploy", Host: "127.0.0.1", Port: port})
		Expect(err).To(MatchError(sshclient.ErrUnreachable))
	})
})
