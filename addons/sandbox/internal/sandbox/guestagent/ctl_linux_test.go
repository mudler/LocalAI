package guestagent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CtlDir", func() {
	It("listens and dials", func() {
		dir := GinkgoT().TempDir()
		c, err := OpenCtlDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Path("agent.sock")).To(And(HavePrefix("/proc/self/fd/"), HaveSuffix("/agent.sock")))
		l, err := c.Listen("agent.sock")
		Expect(err).ToNot(HaveOccurred())
		Expect(filepath.Join(dir, "agent.sock")).To(BeAnExistingFile(), "the socket is not in the folder")
		go func() {
			conn, err := l.Accept()
			if err == nil {
				_ = conn.Close()
			}
		}()
		conn, err := c.Dial("agent.sock")
		Expect(err).ToNot(HaveOccurred())
		_ = conn.Close()
		_ = l.Close()
		_, err = os.Stat(filepath.Join(dir, "agent.sock"))
		Expect(err).To(MatchError(os.ErrNotExist), "closing the listener left the socket file")
		// A stale file from a sandbox that was killed does not block a new listener.
		Expect(os.WriteFile(filepath.Join(dir, "agent.sock"), nil, 0o600)).To(Succeed())
		l2, err := c.Listen("agent.sock")
		Expect(err).ToNot(HaveOccurred(), "listen over a stale file")
		_ = l2.Close()
		_, err = OpenCtlDir(filepath.Join(dir, "missing"))
		Expect(err).To(HaveOccurred(), "OpenCtlDir accepted a missing folder")
	})
})

var _ = Describe("SwitchListener", func() {
	It("stops and starts again", func() {
		c, err := OpenCtlDir(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		checkpoints := 0
		srv := &Server{}
		sl := &SwitchListener{Listen: func() (net.Listener, error) { return c.Listen("agent.sock") }}
		srv.Checkpoint = func() error { checkpoints++; return sl.Stop() }
		Expect(sl.Start(srv)).To(Succeed())
		Expect(sl.Start(srv)).To(Succeed()) // already listening: no second listener
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		call := func() error {
			conn, err := c.Dial("agent.sock")
			if err != nil {
				return err
			}
			return Call(ctx, conn, TypeHealth)
		}
		Expect(call()).To(Succeed())
		conn, err := c.Dial("agent.sock")
		Expect(err).ToNot(HaveOccurred())
		Expect(Call(ctx, conn, TypeCheckpoint)).To(Succeed(), "checkpoint request")
		Expect(checkpoints).To(Equal(1), "hook calls")
		_, err = c.Dial("agent.sock")
		Expect(err).To(HaveOccurred(), "the agent still listens after the checkpoint request")
		Expect(sl.Stop()).To(Succeed()) // stop twice is fine
		Expect(sl.Start(srv)).To(Succeed())
		Expect(call()).To(Succeed(), "after listening again")
		_ = sl.Stop()
	})
})

var _ = Describe("checkpoint and unknown frames", func() {
	It("answers OK to a checkpoint frame without a hook", func() {
		dial := agent(&Server{})
		Expect(Call(context.Background(), dial(), TypeCheckpoint)).To(Succeed())
	})

	It("answers an error to an unknown frame type", func() {
		dial := agent(&Server{})
		err := Call(context.Background(), dial(), "no-such-frame")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown frame type"))
		Expect(Call(context.Background(), dial(), TypeCheckpoint)).To(Succeed(), "checkpoint is not handled")
	})
})

var _ = Describe("loopbackFallback", func() {
	It("ignores the ioctl error when lo is up and keeps it when lo is down", func() {
		boom := errors.New("ENOTTY")
		Expect(loopbackFallback(boom, func() bool { return true })).To(Succeed(), "lo is up, the ioctl error must not matter")
		Expect(loopbackFallback(boom, func() bool { return false })).To(MatchError(boom), "lo is down")
		Expect(loopbackFallback(nil, func() bool { Fail("asked without an error"); return false })).To(Succeed())
	})
})
