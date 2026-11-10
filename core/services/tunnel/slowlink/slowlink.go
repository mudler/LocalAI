// Package slowlink is a TCP proxy for tests that works as one link with a rate
// and a delay. It lets a spec measure how a small call fares during a large
// transfer on a link that behaves like a real one, which loopback does not.
package slowlink

import (
	"net"
	"sync"
	"time"
)

// Link is a proxy in front of one target. It has a rate and a delay in each
// direction, and one queue of 256 KiB for each direction that every connection
// shares. A real link has that shape, and the effect under test depends on it: a
// probe that has its own connection still waits in the queue behind the bytes of
// a transfer on another connection. What a probe must not wait for are the bytes
// that sit in the buffers of its own connection ahead of it.
type Link struct {
	lis    net.Listener
	target string
	rate   float64
	oneWay time.Duration
	up     *queue
	down   *queue
	quit   chan struct{}
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns []net.Conn
}

type item struct {
	data []byte
	dst  net.Conn
	at   time.Time
	// end marks the end of the data of a connection: dst is half-closed after
	// everything before it was delivered.
	end bool
}

// queue is one direction of the link.
type queue struct {
	in       chan item
	delivery chan item
}

// New starts a link on a loopback port that forwards to target. The rate is in
// bytes per second and oneWay is the delay in each direction.
func New(target string, bytesPerSecond float64, oneWay time.Duration) (*Link, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l := &Link{
		lis: lis, target: target, rate: bytesPerSecond, oneWay: oneWay,
		up:   &queue{in: make(chan item, 4), delivery: make(chan item, 4096)},
		down: &queue{in: make(chan item, 4), delivery: make(chan item, 4096)},
		quit: make(chan struct{}),
	}
	for _, q := range []*queue{l.up, l.down} {
		l.wg.Add(2)
		go l.transmit(q)
		go l.deliver(q)
	}
	go l.accept()
	return l, nil
}

// Addr returns the address to dial.
func (l *Link) Addr() string { return l.lis.Addr().String() }

// Close stops the link and closes every connection that goes through it.
func (l *Link) Close() {
	select {
	case <-l.quit:
		return
	default:
	}
	close(l.quit)
	_ = l.lis.Close()
	l.mu.Lock()
	for _, c := range l.conns {
		_ = c.Close()
	}
	l.mu.Unlock()
	l.wg.Wait()
}

func (l *Link) accept() {
	for {
		c, err := l.lis.Accept()
		if err != nil {
			return
		}
		s, err := net.Dial("tcp", l.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		l.mu.Lock()
		l.conns = append(l.conns, c, s)
		l.mu.Unlock()
		go l.read(c, s, l.up)
		go l.read(s, c, l.down)
	}
}

// read moves what arrives on src into the queue of a direction. A full queue
// stops the read, so the socket fills and the sender slows down, as it would on
// a link with a queue of limited size.
func (l *Link) read(src, dst net.Conn, q *queue) {
	for {
		buf := make([]byte, 64<<10)
		n, err := src.Read(buf)
		if n > 0 {
			select {
			case q.in <- item{data: buf[:n], dst: dst}:
			case <-l.quit:
				return
			}
		}
		if err != nil {
			// The end goes through the queue, behind the data. If dst were
			// closed here, the data in the queue would be lost.
			select {
			case q.in <- item{dst: dst, end: true}:
			case <-l.quit:
			}
			return
		}
	}
}

// transmit takes an item out of the queue at the rate of the link.
func (l *Link) transmit(q *queue) {
	defer l.wg.Done()
	next := time.Now()
	for {
		var it item
		select {
		case it = <-q.in:
		case <-l.quit:
			return
		}
		// A late wake-up must not slow the link: the schedule catches up
		// instead. Only an idle link starts a new schedule.
		if now := time.Now(); now.Sub(next) > 5*time.Millisecond {
			next = now
		}
		next = next.Add(time.Duration(float64(len(it.data)) / l.rate * float64(time.Second)))
		if d := time.Until(next); d > 500*time.Microsecond {
			time.Sleep(d)
		}
		it.at = time.Now().Add(l.oneWay)
		select {
		case q.delivery <- it:
		case <-l.quit:
			return
		}
	}
}

// deliver writes an item to its connection after the delay of the link. One
// goroutine for each direction keeps the order of each connection.
func (l *Link) deliver(q *queue) {
	defer l.wg.Done()
	for {
		var it item
		select {
		case it = <-q.delivery:
		case <-l.quit:
			return
		}
		time.Sleep(time.Until(it.at))
		if it.end {
			if tcp, ok := it.dst.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			} else {
				_ = it.dst.Close()
			}
			continue
		}
		_, _ = it.dst.Write(it.data)
	}
}
