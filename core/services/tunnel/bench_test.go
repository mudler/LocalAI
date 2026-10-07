package tunnel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// benchPair is sessionPair for benchmarks, with the upgrader and the dialer
// given by the caller so that a run can use other buffer sizes.
func benchPair(b *testing.B, up *websocket.Upgrader, dialer *websocket.Dialer, lane tunnel.Lane) (frontend, worker *tunnel.Session) {
	b.Helper()
	ch := make(chan *tunnel.Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s, err := tunnel.ServerSession(ws, lane)
		if err != nil {
			_ = ws.Close()
			return
		}
		ch <- s
	}))
	b.Cleanup(srv.Close)
	ws, _, err := dialer.Dial("ws"+srv.URL[len("http"):], nil)
	if err != nil {
		b.Fatal(err)
	}
	worker, err = tunnel.ClientSession(ws, lane)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = worker.Close() })
	frontend = <-ch
	b.Cleanup(func() { _ = frontend.Close() })
	return frontend, worker
}

// BenchmarkTransfer measures one stream that moves 64 MiB between a frontend
// and a worker over a loopback socket, with the websocket buffers of gorilla
// (4 KiB) and the buffers of the tunnel (64 KiB). Push is the worker sending to
// the frontend, which is the direction where the sender masks the payload.
//
//	go test ./core/services/tunnel -run '^$' -bench Transfer -benchtime 5x
func BenchmarkTransfer(b *testing.B) {
	const size = 64 << 20
	buffers := []struct {
		name string
		up   *websocket.Upgrader
		dial *websocket.Dialer
	}{
		{"buffer4KiB", &websocket.Upgrader{}, &websocket.Dialer{HandshakeTimeout: 5 * time.Second}},
		{"buffer64KiB", tunnel.NewUpgrader(), tunnel.NewDialer(5 * time.Second)},
	}
	for _, buf := range buffers {
		for _, dir := range []string{"push", "pull"} {
			b.Run(dir+"/"+buf.name, func(b *testing.B) {
				frontend, worker := benchPair(b, buf.up, buf.dial, tunnel.LaneInference)
				sender, receiver := worker, frontend
				if dir == "pull" {
					sender, receiver = frontend, worker
				}
				chunk := make([]byte, 256<<10)
				b.SetBytes(size)
				b.ResetTimer()
				for range b.N {
					done := make(chan error, 1)
					go func() {
						st, err := receiver.AcceptStream()
						if err != nil {
							done <- err
							return
						}
						_, err = io.Copy(io.Discard, st)
						_ = st.Close()
						done <- err
					}()
					st, err := sender.OpenStream(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					for sent := 0; sent < size; sent += len(chunk) {
						if _, err := st.Write(chunk); err != nil {
							b.Fatal(err)
						}
					}
					if err := st.Close(); err != nil {
						b.Fatal(err)
					}
					if err := <-done; err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
