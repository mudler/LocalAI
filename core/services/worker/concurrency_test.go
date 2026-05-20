package worker

import (
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("backendSupervisor.lockBackend", func() {
	It("serializes operations on the same backend name", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}

		var inflight, peak int32
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				release := s.lockBackend("llama-cpp")
				defer release()

				now := atomic.AddInt32(&inflight, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if now <= p || atomic.CompareAndSwapInt32(&peak, p, now) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&inflight, -1)
			}()
		}
		wg.Wait()

		Expect(atomic.LoadInt32(&peak)).To(Equal(int32(1)),
			"only one goroutine should hold the per-backend lock at a time")
	})

	It("allows different backend names to run in parallel", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}

		var inflight, peak int32
		var wg sync.WaitGroup
		names := []string{"llama-cpp", "vllm", "whisper", "speaker-recognition"}
		for _, n := range names {
			n := n
			wg.Add(1)
			go func() {
				defer wg.Done()
				release := s.lockBackend(n)
				defer release()

				now := atomic.AddInt32(&inflight, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if now <= p || atomic.CompareAndSwapInt32(&peak, p, now) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				atomic.AddInt32(&inflight, -1)
			}()
		}
		wg.Wait()

		Expect(atomic.LoadInt32(&peak)).To(BeNumerically(">=", int32(2)),
			"distinct backends should be able to run concurrently")
	})
})

var _ = Describe("backendSupervisor upgrade handler", func() {
	It("serializes upgrade against install for the same backend name", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}

		var inflight, peak int32
		var wg sync.WaitGroup

		// Simulate one install + one upgrade on the same backend name.
		// The two handlers each acquire lockBackend("llama-cpp"); only one
		// should hold the lock at a time.
		acquire := func() {
			defer wg.Done()
			release := s.lockBackend("llama-cpp")
			defer release()
			now := atomic.AddInt32(&inflight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if now <= p || atomic.CompareAndSwapInt32(&peak, p, now) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inflight, -1)
		}
		wg.Add(2)
		go acquire()
		go acquire()
		wg.Wait()

		Expect(atomic.LoadInt32(&peak)).To(Equal(int32(1)))
	})
})
