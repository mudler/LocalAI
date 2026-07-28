package main

import "time"

// batchRequest is one in-flight unary transcription waiting to be batched.
// In production pcm/decoder are set; tag is an opaque marker used by tests.
type batchRequest struct {
	pcm     []float32
	decoder int32
	// language is the per-request target locale ("" means the model default).
	// parakeet.cpp's batched C-API takes ONE target_lang for the whole batch,
	// so the dispatcher only coalesces requests that share a language.
	language string
	tag      string
	reply    chan batchReply
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
//
// A batch carries ONE language (parakeet.cpp's batched C-API takes a single
// target_lang), so a request whose language differs from the batch leader is
// not coalesced: it is held in carry and becomes the leader of the next batch.
// carry is therefore never dropped and its caller never deadlocks: every batch
// (including a lone carry on stop) is dispatched, and runBatch replies to all.
func (b *batcher) run(stop <-chan struct{}) {
	var carry *batchRequest
	for {
		var first *batchRequest
		if carry != nil {
			// A mismatched request from the previous fill leads this batch.
			first, carry = carry, nil
		} else {
			select {
			case first = <-b.submit:
			case <-stop:
				return
			}
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
				if r.language != first.language {
					// Different language: carry it to the next batch so this
					// batch stays single-language, then dispatch what we have.
					carry = r
					break fill
				}
				batch = append(batch, r)
			case <-timer.C:
				break fill
			case <-stop:
				timer.Stop()
				b.runBatch(batch)
				// Don't strand a carried request's caller on shutdown.
				if carry != nil {
					b.runBatch([]*batchRequest{carry})
				}
				return
			}
		}
		timer.Stop()
		b.runBatch(batch)
	}
}
