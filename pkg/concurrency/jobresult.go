package concurrency

import (
	"context"
	"sync"
)

// This is a Read-ONLY structure that contains the result of an arbitrary asynchronous action
type JobResult[RequestType any, ResultType any] struct {
	request *RequestType
	result  *ResultType
	err     error
	once    sync.Once
	done    *chan struct{}
}

// This structure is returned in a pair with a JobResult and serves as the structure that has access to be updated.
type WritableJobResult[RequestType any, ResultType any] struct {
	*JobResult[RequestType, ResultType]
}

// Wait blocks until the result is ready and then returns the result, or the context expires.
// Returns *ResultType instead of ResultType since its possible we have only an error and nil for ResultType.
// Is this correct and idiomatic?
func (jr *JobResult[RequestType, ResultType]) Wait(ctx context.Context) (*ResultType, error) {
	if jr.done == nil { // If the channel is blanked out, result is ready.
		return jr.result, jr.err
	}
	select {
	case <-*jr.done: // Wait for the result to be ready
		jr.done = nil
		if jr.err != nil {
			return nil, jr.err
		}
		return jr.result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Accessor function to allow holders of JobResults to access the associated request, without allowing the pointer to be updated.
func (jr *JobResult[RequestType, ResultType]) Request() *RequestType {
	return jr.request
}

// This is the function that actually updates the Result and Error on the JobResult... but it's normally not accessible
func (jr *JobResult[RequestType, ResultType]) setResult(result ResultType, err error) {
	jr.once.Do(func() {
		jr.result = &result
		jr.err = err
		close(*jr.done) // Signal that the result is ready - since this is only ran once, jr.done cannot be set to nil yet.
	})
}

// Only the WritableJobResult can actually call setResult - prevents accidental corruption
func (wjr *WritableJobResult[RequestType, ResultType]) SetResult(result ResultType, err error) {
	wjr.JobResult.setResult(result, err)
}

// NewJobResult binds a request to a matched pair of JobResult and WritableJobResult
func NewJobResult[RequestType any, ResultType any](request RequestType) (*JobResult[RequestType, ResultType], *WritableJobResult[RequestType, ResultType]) {
	done := make(chan struct{})
	jr := &JobResult[RequestType, ResultType]{
		once:    sync.Once{},
		request: &request,
		done:    &done,
	}
	return jr, &WritableJobResult[RequestType, ResultType]{JobResult: jr}
}
