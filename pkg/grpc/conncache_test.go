package grpc

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// countingListener counts the connections that a server accepts, so a spec can
// tell one shared connection from one connection per call.
type countingListener struct {
	net.Listener
	accepted atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return c, err
}

// serveCounting serves the dummy model on addr ("127.0.0.1:0" for any port)
// and returns the listener and a stop function.
func serveCounting(addr string) (*countingListener, func()) {
	lis, err := net.Listen("tcp", addr)
	Expect(err).ToNot(HaveOccurred())
	cl := &countingListener{Listener: lis}
	s := gogrpc.NewServer(serverOpts()...)
	pb.RegisterBackendServer(s, &server{llm: &dummyModel{}})
	go func() { _ = s.Serve(cl) }()
	var once sync.Once
	stop := func() { once.Do(s.Stop) }
	DeferCleanup(stop)
	return cl, stop
}

var _ = Describe("The shared connection of a client", func() {
	It("serves every call over one connection", func() {
		lis, _ := serveCounting("127.0.0.1:0")
		client := NewClient(lis.Addr().String(), false, nil, false)
		DeferCleanup(func() { CloseBackend(client) })

		for range 20 {
			ok, err := client.HealthCheck(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
		}
		Expect(lis.accepted.Load()).To(Equal(int32(1)))
	})

	It("serves parallel calls over one connection", func() {
		lis, _ := serveCounting("127.0.0.1:0")
		client := NewClient(lis.Addr().String(), true, nil, false)
		DeferCleanup(func() { CloseBackend(client) })

		var wg sync.WaitGroup
		var failures atomic.Int32
		for range 16 {
			wg.Go(func() {
				for range 10 {
					if ok, err := client.HealthCheck(context.Background()); err != nil || !ok {
						failures.Add(1)
					}
				}
			})
		}
		wg.Wait()
		Expect(failures.Load()).To(BeZero())
		Expect(lis.accepted.Load()).To(Equal(int32(1)))
		Expect(client.IsBusy()).To(BeFalse())
	})

	It("reaches a backend that restarted on the same address at once", func() {
		lis, stop := serveCounting("127.0.0.1:0")
		addr := lis.Addr().String()
		client := NewClient(addr, false, nil, false)
		DeferCleanup(func() { CloseBackend(client) })

		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())

		stop()
		short, cancel := context.WithTimeout(context.Background(), time.Second)
		ok, _ = client.HealthCheck(short)
		cancel()
		Expect(ok).To(BeFalse(), "the backend is down")

		// gRPC would wait out a reconnect backoff here. The client drops the
		// failed connection instead, so the first call after the restart dials.
		serveCounting(addr)
		start := time.Now()
		ok, err = client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond))
	})

	It("keeps working after Close, with a connection per call", func() {
		lis, _ := serveCounting("127.0.0.1:0")
		client := NewClient(lis.Addr().String(), false, nil, false)

		_, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		CloseBackend(client)

		for range 3 {
			ok, err := client.HealthCheck(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
		}
		Expect(lis.accepted.Load()).To(Equal(int32(4)))
	})
})

var _ = Describe("connCache", func() {
	var lis *countingListener
	var dial func(...gogrpc.DialOption) (*gogrpc.ClientConn, error)

	BeforeEach(func() {
		lis, _ = serveCounting("127.0.0.1:0")
		c := &Client{address: lis.Addr().String()}
		dial = c.dial
	})

	It("closes a connection only when its last call releases it", func() {
		var cache connCache
		cc, release, err := cache.acquire(dial)
		Expect(err).ToNot(HaveOccurred())
		cc.Connect()
		Eventually(cc.GetState, 5*time.Second).Should(Equal(connectivity.Ready))

		cache.close()
		Expect(cc.GetState()).ToNot(Equal(connectivity.Shutdown), "a call still uses it")

		release()
		Expect(cc.GetState()).To(Equal(connectivity.Shutdown))
		release() // a second release is a no-op
	})

	It("hands out a new connection after one was retired", func() {
		var cache connCache
		first, release1, err := cache.acquire(dial)
		Expect(err).ToNot(HaveOccurred())

		cache.retire(first)
		second, release2, err := cache.acquire(dial)
		Expect(err).ToNot(HaveOccurred())
		Expect(second).ToNot(BeIdenticalTo(first))
		Expect(first.GetState()).ToNot(Equal(connectivity.Shutdown))

		release1()
		Expect(first.GetState()).To(Equal(connectivity.Shutdown))
		release2()
		Expect(second.GetState()).ToNot(Equal(connectivity.Shutdown), "the current connection stays open")
		cache.close()
		Expect(second.GetState()).To(Equal(connectivity.Shutdown))
	})

	It("ignores the retirement of a connection that is no longer current", func() {
		var cache connCache
		first, release1, _ := cache.acquire(dial)
		release1()
		cache.retire(first)
		second, release2, _ := cache.acquire(dial)
		release2()
		cache.retire(first)
		third, release3, _ := cache.acquire(dial)
		release3()
		Expect(third).To(BeIdenticalTo(second))
		cache.close()
	})
})
