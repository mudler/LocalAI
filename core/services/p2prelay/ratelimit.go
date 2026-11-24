package p2prelay

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
	"golang.org/x/time/rate"
)

// RateLimitedHost wraps h for relay.New so that every relayed circuit is
// limited to perCircuit bytes per second in each direction, and all circuits
// together to total bytes per second. Zero means no limit; with both zero h
// is returned as is.
//
// go-libp2p v0.48.0 has no rate hook in the circuit v2 relay, but the relay
// receives the source leg of a circuit on the hop handler it registers with
// SetStreamHandler and opens the destination leg with NewStream on the stop
// protocol. The wrapper hands it rate-limited copies of both streams, so the
// relay's copy loop reads through a token bucket per stream (that is, per
// circuit and direction) and through the shared total bucket. Only relay
// streams are wrapped; every other protocol of h is untouched.
func RateLimitedHost(h host.Host, perCircuit, total int64) host.Host {
	if perCircuit <= 0 && total <= 0 {
		return h
	}
	return newRateLimitedHost(h, perCircuit, total)
}

// newRateLimitedHost is RateLimitedHost that always wraps, so that the
// per-circuit limit can be changed later with setPerCircuit.
func newRateLimitedHost(h host.Host, perCircuit, total int64) *rateLimitedHost {
	r := &rateLimitedHost{Host: h}
	r.perCircuit.Store(perCircuit)
	if total > 0 {
		r.total = newLimiter(total)
	}
	return r
}

type rateLimitedHost struct {
	host.Host
	// perCircuit is read for every new stream, so a change reaches the next
	// circuit and leaves the running ones alone.
	perCircuit atomic.Int64
	total      *rate.Limiter
}

func (r *rateLimitedHost) setPerCircuit(bps int64) { r.perCircuit.Store(bps) }

// SetStreamHandler wraps the streams of the hop protocol.
func (r *rateLimitedHost) SetStreamHandler(pid protocol.ID, handler network.StreamHandler) {
	if pid != proto.ProtoIDv2Hop {
		r.Host.SetStreamHandler(pid, handler)
		return
	}
	r.Host.SetStreamHandler(pid, func(s network.Stream) { handler(r.wrap(s)) })
}

// NewStream wraps the streams of the stop protocol.
func (r *rateLimitedHost) NewStream(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
	s, err := r.Host.NewStream(ctx, p, pids...)
	if err != nil || !slices.Contains(pids, proto.ProtoIDv2Stop) {
		return s, err
	}
	return r.wrap(s), nil
}

func (r *rateLimitedHost) wrap(s network.Stream) network.Stream {
	ls := &limitedStream{Stream: s, total: r.total}
	if bps := r.perCircuit.Load(); bps > 0 {
		ls.own = newLimiter(bps)
	}
	ls.chunk = burst(ls.own, ls.total)
	ls.ctx, ls.cancel = context.WithCancel(context.Background())
	return ls
}

// newLimiter returns a token bucket of bps bytes per second. The burst is a
// quarter of a second of traffic, between 1 KiB and 32 KiB, so a circuit
// cannot run ahead of its rate for long.
func newLimiter(bps int64) *rate.Limiter {
	b := min(max(bps/4, 1<<10), 32<<10)
	return rate.NewLimiter(rate.Limit(bps), int(b))
}

func burst(ls ...*rate.Limiter) int {
	n := 32 << 10
	for _, l := range ls {
		if l != nil {
			n = min(n, l.Burst())
		}
	}
	return n
}

// limitedStream charges every byte it reads to its own bucket and to the
// shared one. Reads are cut to the burst size, so one read never needs more
// tokens than a bucket holds.
type limitedStream struct {
	network.Stream
	own, total *rate.Limiter
	chunk      int

	ctx    context.Context
	cancel context.CancelFunc
}

func (s *limitedStream) Read(p []byte) (int, error) {
	if len(p) > s.chunk {
		p = p[:s.chunk]
	}
	n, err := s.Stream.Read(p)
	if n > 0 {
		for _, l := range []*rate.Limiter{s.own, s.total} {
			if l == nil {
				continue
			}
			if werr := l.WaitN(s.ctx, n); werr != nil && err == nil {
				err = werr
			}
		}
	}
	return n, err
}

func (s *limitedStream) Close() error {
	s.cancel()
	return s.Stream.Close()
}

func (s *limitedStream) Reset() error {
	s.cancel()
	return s.Stream.Reset()
}

func (s *limitedStream) ResetWithError(code network.StreamErrorCode) error {
	s.cancel()
	return s.Stream.ResetWithError(code)
}

func (s *limitedStream) CloseRead() error {
	s.cancel()
	return s.Stream.CloseRead()
}
