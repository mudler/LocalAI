package tunnel_test

import (
	"context"
	"errors"
	"io"
	"net"
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

var _ = Describe("Streams that the worker opens", func() {
	// The frontend reads no stream that the worker opens. A stream that the
	// session accepted would hold its window of unread data for as long as the
	// session lives, and a worker that opens many of them would make the
	// frontend hold memory for each.
	for _, lane := range []tunnel.Lane{tunnel.LaneInference, tunnel.LaneBulk} {
		It("resets every one of them on the "+string(lane)+" lane and accepts none", func() {
			server, client := sessionPair(lane)
			accepted := make(chan struct{}, 1)
			go func() {
				if _, err := server.AcceptStream(); err == nil {
					accepted <- struct{}{}
				}
			}()

			// More streams than the default limit of yamux for incoming streams.
			const streams = 1100
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			opened := make([]net.Conn, 0, streams)
			for range streams {
				st, err := client.OpenStream(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = st.Write([]byte("unread"))
				Expect(err).ToNot(HaveOccurred())
				opened = append(opened, st)
			}
			for i, st := range opened {
				Expect(st.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
				_, err := st.Read(make([]byte, 1))
				Expect(err).To(HaveOccurred(), "stream %d", i)
				var netErr net.Error
				if errors.As(err, &netErr) {
					Expect(netErr.Timeout()).To(BeFalse(), "stream %d was not reset: %v", i, err)
				}
			}
			Consistently(accepted, "200ms").ShouldNot(Receive())

			// The frontend still opens streams to the worker.
			st, err := server.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = st.Close() })
			got, err := client.AcceptStream()
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Close()).To(Succeed())
		})
	}
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
