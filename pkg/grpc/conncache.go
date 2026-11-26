package grpc

import (
	"context"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// connIdleTimeout is how long a shared connection may carry no call before
// gRPC drops its transport. A client that nobody closes (a one-off probe, a
// client replaced without a Close) then holds no socket for long. The next call
// on an idle connection reconnects, which costs one dial, as every call did
// before connections were shared.
const connIdleTimeout = time.Minute

// sharedConn is one gRPC connection and the number of calls that use it.
type sharedConn struct {
	cc      *grpc.ClientConn
	users   int
	retired bool
}

// connCache keeps one gRPC connection per client and gives it to every call.
// Opening a connection for each call costs a TCP and an HTTP/2 handshake on
// every request, which shows in the time to first token of short requests and
// in the rate of small calls such as embeddings.
//
// A connection that a call found unavailable is retired: no new call gets it,
// and it closes when its last call ends. The next call dials again at once.
// gRPC would otherwise reconnect by itself with an exponential backoff, and a
// backend that starts or restarts on the same address would be refused for up
// to two minutes. After close, every call dials its own connection and closes
// it at the end, so a client that is still used after a Close keeps working
// and leaks nothing.
type connCache struct {
	mu     sync.Mutex
	cur    *sharedConn
	closed bool
}

// acquire returns a connection and a release function that the caller must
// call once, when it no longer uses the connection.
func (cache *connCache) acquire(dial func(...grpc.DialOption) (*grpc.ClientConn, error)) (*grpc.ClientConn, func(), error) {
	cache.mu.Lock()
	if cache.closed {
		cache.mu.Unlock()
		cc, err := dial()
		if err != nil {
			return nil, nil, err
		}
		return cc, func() { _ = cc.Close() }, nil
	}
	if cache.cur == nil {
		// grpc.NewClient does no I/O, so dialing under the lock blocks nobody.
		cc, err := dial(
			grpc.WithIdleTimeout(connIdleTimeout),
			grpc.WithChainUnaryInterceptor(cache.retireUnavailableUnary),
			grpc.WithChainStreamInterceptor(cache.retireUnavailableStream),
		)
		if err != nil {
			cache.mu.Unlock()
			return nil, nil, err
		}
		cache.cur = &sharedConn{cc: cc}
	}
	sc := cache.cur
	sc.users++
	cache.mu.Unlock()

	var once sync.Once
	return sc.cc, func() { once.Do(func() { cache.release(sc) }) }, nil
}

func (cache *connCache) release(sc *sharedConn) {
	cache.mu.Lock()
	sc.users--
	closeNow := sc.retired && sc.users == 0
	cache.mu.Unlock()
	if closeNow {
		_ = sc.cc.Close()
	}
}

// retire stops handing out cc. It closes when its last call ends.
func (cache *connCache) retire(cc *grpc.ClientConn) {
	cache.mu.Lock()
	sc := cache.cur
	if sc == nil || sc.cc != cc {
		cache.mu.Unlock()
		return
	}
	cache.cur = nil
	sc.retired = true
	closeNow := sc.users == 0
	cache.mu.Unlock()
	if closeNow {
		_ = sc.cc.Close()
	}
}

// close retires the shared connection and makes later calls dial their own.
func (cache *connCache) close() {
	cache.mu.Lock()
	cache.closed = true
	sc := cache.cur
	cache.cur = nil
	closeNow := false
	if sc != nil {
		sc.retired = true
		closeNow = sc.users == 0
	}
	cache.mu.Unlock()
	if closeNow {
		_ = sc.cc.Close()
	}
}

func (cache *connCache) retireUnavailableUnary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if status.Code(err) == codes.Unavailable {
		cache.retire(cc)
	}
	return err
}

func (cache *connCache) retireUnavailableStream(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	s, err := streamer(ctx, desc, cc, method, opts...)
	if status.Code(err) == codes.Unavailable {
		cache.retire(cc)
	}
	return s, err
}

// CloseBackend closes the connection of b if b has one. A caller that built a
// client for a single probe calls it when done: the client keeps its
// connection open between calls, and an unclosed one holds a socket until the
// idle timeout.
func CloseBackend(b Backend) {
	if closer, ok := b.(io.Closer); ok {
		_ = closer.Close() // the client is discarded; a close error changes nothing
	}
}
