// SPDX-License-Identifier: MIT
package systemone

import (
	"context"
	"errors"
	"sync"
)

// MaxAdmissions bounds decision-owned buffering, decoding and retained request
// bodies across HTTP and internal callers. Leases last until actual work ends,
// not merely until validation or a cancelled caller returns. This is separate
// from the backend's abandoned-operation ceiling, and does not bound model RSS
// or memory already owned by callers/upstream middleware.
const MaxAdmissions = 8

var admissions = make(chan struct{}, MaxAdmissions)
var ErrAdmissionCapacity = errors.New("decision admission capacity reached")

// AcquireAdmission fails promptly on saturation: no unbounded waiter queue.
// Validation helpers do not acquire leases, avoiding nested acquisition.
func AcquireAdmission(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case admissions <- struct{}{}:
	default:
		return nil, ErrAdmissionCapacity
	}
	var once sync.Once
	release := func() { once.Do(func() { <-admissions }) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
