package main

import "time"

// batchRequest is one in-flight unary transcription waiting to be batched.
// In production pcm/decoder are set; tag is an opaque marker used by tests.
type batchRequest struct {
	pcm     []float32
	decoder int32
	tag     string
	reply   chan batchReply
}

// batchReply carries one per-item JSON object string (an element of the C-API's
// JSON array) or an error back to the waiting handler goroutine.
type batchReply struct {
	json string
	err  error
}

// batcher coalesces concurrent batchRequests into batched runBatch calls. A
// single run() goroutine is the sole caller of runBatch, so runBatch (which in
// production calls the thread-unsafe C engine) is never entered concurrently.
type batcher struct {
	submit   chan *batchRequest
	maxSize  int
	maxWait  time.Duration
	runBatch func(reqs []*batchRequest) // must deliver a reply to every req
}

func newBatcher(maxSize int, maxWait time.Duration, runBatch func([]*batchRequest)) *batcher {
	if maxSize < 1 {
		maxSize = 1
	}
	return &batcher{
		submit:   make(chan *batchRequest),
		maxSize:  maxSize,
		maxWait:  maxWait,
		runBatch: runBatch,
	}
}

// run is the dispatcher loop: accumulate submitted requests until either maxSize
// is reached or maxWait elapses since the first queued request, then dispatch.
// Exits when stop is closed (draining any partially-filled batch first).
func (b *batcher) run(stop <-chan struct{}) {
	for {
		var first *batchRequest
		select {
		case first = <-b.submit:
		case <-stop:
			return
		}
		batch := []*batchRequest{first}

		// maxSize==1 disables batching: dispatch immediately (passthrough).
		if b.maxSize == 1 {
			b.runBatch(batch)
			continue
		}

		timer := time.NewTimer(b.maxWait)
	fill:
		for len(batch) < b.maxSize {
			select {
			case r := <-b.submit:
				batch = append(batch, r)
			case <-timer.C:
				break fill
			case <-stop:
				timer.Stop()
				b.runBatch(batch)
				return
			}
		}
		timer.Stop()
		b.runBatch(batch)
	}
}
