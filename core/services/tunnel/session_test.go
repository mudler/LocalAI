package tunnel_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Buffers of the websocket", func() {
	It("gives the upgrader and the dialer 64 KiB for reading and writing", func() {
		up := tunnel.NewUpgrader()
		Expect(up.ReadBufferSize).To(Equal(64 << 10))
		Expect(up.WriteBufferSize).To(Equal(64 << 10))

		d := tunnel.NewDialer(3 * time.Second)
		Expect(d.ReadBufferSize).To(Equal(64 << 10))
		Expect(d.WriteBufferSize).To(Equal(64 << 10))
		Expect(d.HandshakeTimeout).To(Equal(3 * time.Second))
	})

	It("is the only way the files of the tunnel make a websocket", func() {
		// A literal websocket.Upgrader or websocket.Dialer gets the 4 KiB buffers
		// of gorilla, and a transfer from the worker to the frontend runs five
		// times slower than the other direction. The files that open a websocket
		// of the tunnel must use NewUpgrader and NewDialer.
		var files []string
		for _, pattern := range []string{"../../http/endpoints/cluster/*.go", "../worker/tunnel*.go"} {
			matches, err := filepath.Glob(pattern)
			Expect(err).ToNot(HaveOccurred())
			files = append(files, matches...)
		}
		files = slices.DeleteFunc(files, func(f string) bool { return strings.HasSuffix(f, "_test.go") })
		Expect(files).ToNot(BeEmpty(), "the pattern found no file, so it checks nothing")
		for _, f := range files {
			src, err := os.ReadFile(f)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(src)).ToNot(ContainSubstring("websocket.Upgrader{"), f)
			Expect(string(src)).ToNot(ContainSubstring("websocket.Dialer{"), f)
		}
	})

	It("keeps the default origin check so that a browser of another origin is refused", func() {
		Expect(tunnel.NewUpgrader().CheckOrigin).To(BeNil())
	})
})

var _ = Describe("Lanes", func() {
	It("reads the lane parameter, and reads an empty one as the inference lane", func() {
		lane, err := tunnel.ParseLane("")
		Expect(err).ToNot(HaveOccurred())
		Expect(lane).To(Equal(tunnel.LaneInference))

		lane, err = tunnel.ParseLane("bulk")
		Expect(err).ToNot(HaveOccurred())
		Expect(lane).To(Equal(tunnel.LaneBulk))

		_, err = tunnel.ParseLane("express")
		Expect(err).To(HaveOccurred())
	})

	It("keeps the yamux defaults for the inference lane", func() {
		cfg := tunnel.SessionConfigFor(tunnel.LaneInference)
		Expect(cfg.InitialStreamWindowSize).To(BeEquivalentTo(256 << 10))
		Expect(cfg.MaxStreamWindowSize).To(BeEquivalentTo(16 << 20))
	})

	It("uses windows of 4 MiB and 32 MiB for the bulk lane", func() {
		cfg := tunnel.SessionConfigFor(tunnel.LaneBulk)
		Expect(cfg.InitialStreamWindowSize).To(BeEquivalentTo(4 << 20))
		Expect(cfg.MaxStreamWindowSize).To(BeEquivalentTo(32 << 20))
	})

	It("returns a new configuration for every call", func() {
		a := tunnel.SessionConfigFor(tunnel.LaneBulk)
		b := tunnel.SessionConfigFor(tunnel.LaneBulk)
		Expect(a).ToNot(BeIdenticalTo(b))
	})
})

var _ = Describe("Session", func() {
	for _, lane := range []tunnel.Lane{tunnel.LaneInference, tunnel.LaneBulk} {
		It("carries a stream from the frontend to the worker and back on the "+string(lane)+" lane", func() {
			frontend, worker := sessionPair(lane)

			done := make(chan error, 1)
			go func() {
				st, err := worker.AcceptStream()
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = st.Close() }()
				tag, target, err := tunnel.ReadStreamRequest(st)
				if err != nil {
					done <- err
					return
				}
				if tag != tunnel.StreamTagGRPC || target != "127.0.0.1:50052" {
					done <- io.ErrUnexpectedEOF
					return
				}
				if err := tunnel.WriteStreamAccepted(st); err != nil {
					done <- err
					return
				}
				_, err = io.Copy(st, st)
				done <- err
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			st, err := frontend.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tunnel.WriteStreamRequest(st, tunnel.StreamTagGRPC, "127.0.0.1:50052")).To(Succeed())
			Expect(tunnel.ReadStreamReply(st)).To(Succeed())
			_, err = st.Write([]byte("hello"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 5)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("hello"))
			Expect(st.Close()).To(Succeed())
			Eventually(done).Should(Receive(BeNil()))
		})
	}

	It("moves a payload larger than the buffers of the websocket without loss", func() {
		frontend, worker := sessionPair(tunnel.LaneBulk)
		payload := make([]byte, 5<<20+17)
		for i := range payload {
			payload[i] = byte(i * 31)
		}

		echoed := make(chan error, 1)
		go func() {
			st, err := worker.AcceptStream()
			if err != nil {
				echoed <- err
				return
			}
			_, err = io.Copy(st, st)
			_ = st.Close()
			echoed <- err
		}()

		st, err := frontend.OpenStream(context.Background())
		Expect(err).ToNot(HaveOccurred())
		written := make(chan error, 1)
		go func() {
			_, werr := st.Write(payload)
			written <- werr
		}()
		got := make([]byte, len(payload))
		_, err = io.ReadFull(st, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(payload))
		Eventually(written).Should(Receive(BeNil()))
		Expect(st.Close()).To(Succeed())
		Eventually(echoed).Should(Receive())
	})

	It("ends the other side when one side closes", func() {
		frontend, worker := sessionPair(tunnel.LaneInference)
		Expect(frontend.Close()).To(Succeed())
		Eventually(worker.CloseChan()).Should(BeClosed())
		Expect(worker.IsClosed()).To(BeTrue())
		_, err := worker.AcceptStream()
		Expect(err).To(HaveOccurred())
	})
})
