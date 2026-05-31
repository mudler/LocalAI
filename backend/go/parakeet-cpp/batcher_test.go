package main

import (
	"sync"
	"testing"
	"time"
)

func TestBatcherCoalescesConcurrentSubmits(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	run := func(reqs []*batchRequest) {
		mu.Lock()
		sizes = append(sizes, len(reqs))
		mu.Unlock()
		for _, r := range reqs {
			r.reply <- batchReply{json: r.tag}
		}
	}
	b := newBatcher(4, 50*time.Millisecond, run)
	stop := make(chan struct{})
	go b.run(stop)
	defer close(stop)

	const N = 4
	var wg sync.WaitGroup
	got := make([]string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep := make(chan batchReply, 1)
			b.submit <- &batchRequest{tag: string(rune('a' + i)), reply: rep}
			got[i] = (<-rep).json
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	total, maxBatch := 0, 0
	for _, s := range sizes {
		total += s
		if s > maxBatch {
			maxBatch = s
		}
	}
	if total != N {
		t.Fatalf("handled %d, want %d", total, N)
	}
	if maxBatch < 2 {
		t.Fatalf("no coalescing: max batch %d", maxBatch)
	}
}

func TestBatcherSizeTrigger(t *testing.T) {
	dispatched := make(chan int, 8)
	run := func(reqs []*batchRequest) {
		dispatched <- len(reqs)
		for _, r := range reqs {
			r.reply <- batchReply{json: r.tag}
		}
	}
	b := newBatcher(2, time.Hour, run) // huge window: only size triggers
	stop := make(chan struct{})
	go b.run(stop)
	defer close(stop)
	for i := 0; i < 2; i++ {
		rep := make(chan batchReply, 1)
		b.submit <- &batchRequest{tag: "x", reply: rep}
		go func(rep chan batchReply) { <-rep }(rep)
	}
	select {
	case n := <-dispatched:
		if n != 2 {
			t.Fatalf("size trigger batch = %d, want 2", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("size trigger did not fire")
	}
}

func TestBatcherWindowTrigger(t *testing.T) {
	dispatched := make(chan int, 8)
	run := func(reqs []*batchRequest) {
		dispatched <- len(reqs)
		for _, r := range reqs {
			r.reply <- batchReply{json: r.tag}
		}
	}
	b := newBatcher(8, 20*time.Millisecond, run) // size never reached; window fires
	stop := make(chan struct{})
	go b.run(stop)
	defer close(stop)
	rep := make(chan batchReply, 1)
	b.submit <- &batchRequest{tag: "x", reply: rep}
	go func() { <-rep }()
	select {
	case n := <-dispatched:
		if n != 1 {
			t.Fatalf("window batch = %d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("window trigger did not fire")
	}
}

func TestBatcherSizeOneBypass(t *testing.T) {
	dispatched := make(chan int, 8)
	run := func(reqs []*batchRequest) {
		dispatched <- len(reqs)
		for _, r := range reqs {
			r.reply <- batchReply{json: r.tag}
		}
	}
	b := newBatcher(1, time.Hour, run) // size 1 => immediate per-request dispatch
	stop := make(chan struct{})
	go b.run(stop)
	defer close(stop)
	rep := make(chan batchReply, 1)
	b.submit <- &batchRequest{tag: "x", reply: rep}
	go func() { <-rep }()
	select {
	case n := <-dispatched:
		if n != 1 {
			t.Fatalf("size-1 batch = %d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("size-1 dispatch did not fire")
	}
}
