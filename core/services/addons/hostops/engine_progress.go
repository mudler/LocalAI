// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// The channel is request and response with a bound on each call, so progress
// is a bounded wait: the caller loops. The wait stays under the 30 s read
// timeout of the channel with a margin.
const maxProgressWait = 25 * time.Second

// Progress is one reading of an engine job.
type Progress struct {
	JobID, Status, Message string
	Percent                float32
	Done                   bool
}

// final reports whether a job status is last: no further event follows.
func final(status string) bool {
	return status == "completed" || status == "failed" || status == "stopped"
}

// ProgressSource is what a job service offers the progress and stop
// operations. Every call names the user; a job the user does not hold is
// ErrNotFound.
type ProgressSource interface {
	// StreamProgress calls cb for each event of the job until the job ends or
	// ctx is done.
	StreamProgress(ctx context.Context, user, jobID string, cb func(Progress)) error
	// Snapshot returns the current state of the job.
	Snapshot(user, jobID string) (Progress, error)
	// StopJob stops the job. saveCheckpoint is ignored by a job kind that has
	// no checkpoint.
	StopJob(ctx context.Context, user, jobID string, saveCheckpoint bool) error
}

// Progresser reads and stops the jobs of one kind.
type Progresser interface {
	// Next waits at most wait for the next event of the job and returns the
	// latest one. Without an event it returns the snapshot when the wait ends.
	Next(ctx context.Context, user, jobID string, wait time.Duration) (Progress, error)
	Stop(ctx context.Context, user, jobID string, saveCheckpoint bool) error
}

// NewProgresser builds the bounded-wait reader over a job service.
func NewProgresser(src ProgressSource) Progresser { return progresser{src: src} }

type progresser struct{ src ProgressSource }

func (p progresser) Next(ctx context.Context, user, jobID string, wait time.Duration) (Progress, error) {
	snap, err := p.src.Snapshot(user, jobID)
	if err != nil {
		return Progress{}, err
	}
	if final(snap.Status) {
		snap.Done = true
		return snap, nil
	}

	// The stream ends with the wait, and at once after the first event, so the
	// service stops sending.
	sctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	var mu sync.Mutex
	var latest *Progress
	signal := make(chan struct{}, 1)
	cb := func(ev Progress) {
		mu.Lock()
		latest = &ev
		mu.Unlock()
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	last := func() *Progress {
		mu.Lock()
		defer mu.Unlock()
		return latest
	}
	streamErr := make(chan error, 1)
	go func() { streamErr <- p.src.StreamProgress(sctx, user, jobID, cb) }()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-signal:
		return withDone(*last()), nil
	case err := <-streamErr:
		if ev := last(); ev != nil {
			return withDone(*ev), nil
		}
		if ctx.Err() != nil {
			return Progress{}, ctx.Err()
		}
		// Our own wait ending the stream is the normal case, not a failure.
		if err != nil && sctx.Err() == nil {
			return Progress{}, fmt.Errorf("progress: %w", err)
		}
	case <-ctx.Done():
		return Progress{}, ctx.Err()
	case <-timer.C:
	}
	// The wait ended without an event, or the stream ended without one.
	cur, err := p.src.Snapshot(user, jobID)
	if err != nil {
		return Progress{}, err
	}
	return withDone(cur), nil
}

func (p progresser) Stop(ctx context.Context, user, jobID string, saveCheckpoint bool) error {
	// The check is the service's own, but it answers in plain errors; a job the
	// user does not hold is not found here, the same as a read.
	if _, err := p.src.Snapshot(user, jobID); err != nil {
		return err
	}
	return p.src.StopJob(ctx, user, jobID, saveCheckpoint)
}

func withDone(p Progress) Progress {
	p.Done = final(p.Status)
	return p
}

// RegisterEngineProgress registers the progress reads and the stop operations.
// A nil progresser registers nothing for its kind.
func RegisterEngineProgress(b *Broker, ft, q Progresser) error {
	if ft != nil {
		if err := registerProgressOps(b, "finetune", featureFineTuning, ft); err != nil {
			return err
		}
	}
	if q != nil {
		if err := registerProgressOps(b, "quantize", featureQuantization, q); err != nil {
			return err
		}
	}
	return nil
}

func registerProgressOps(b *Broker, kind, feature string, p Progresser) error {
	for _, s := range []Spec{
		{Op: kind + ".progress", Scope: "read", Feature: feature, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			var a struct {
				JobID       string `json:"jobId"`
				WaitSeconds int    `json:"waitSeconds"`
			}
			if err := decodeArgs(c.Arguments, &a); err != nil {
				return nil, err
			}
			if err := checkJobID(a.JobID); err != nil {
				return nil, err
			}
			if a.WaitSeconds < 0 {
				return nil, fmt.Errorf("%w: waitSeconds must not be negative", ErrBadRequest)
			}
			wait := min(time.Duration(a.WaitSeconds)*time.Second, maxProgressWait)
			ev, err := p.Next(ctx, userKey(c), a.JobID, wait)
			if err != nil {
				return nil, err
			}
			return marshal(struct {
				JobID   string  `json:"jobId"`
				Status  string  `json:"status"`
				Message string  `json:"message,omitempty"`
				Percent float32 `json:"percent"`
				Done    bool    `json:"done"`
			}{ev.JobID, ev.Status, scrub(ev.Message), ev.Percent, ev.Done})
		}},
		{Op: kind + ".stop", Scope: "apply", Feature: feature, Idempotent: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			jobID, save, err := stopArgs(kind, c.Arguments)
			if err != nil {
				return nil, err
			}
			if err := p.Stop(ctx, userKey(c), jobID, save); err != nil {
				return nil, err
			}
			return marshal(map[string]any{"jobId": jobID, "stopRequested": true})
		}},
	} {
		if err := b.Register(s); err != nil {
			return err
		}
	}
	return nil
}

// stopArgs decodes a stop call. A quantization job has no checkpoint, so its
// closed argument set has no saveCheckpoint.
func stopArgs(kind string, raw json.RawMessage) (string, bool, error) {
	if kind == "quantize" {
		var a struct {
			JobID string `json:"jobId"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return "", false, err
		}
		return a.JobID, false, checkJobID(a.JobID)
	}
	var a struct {
		JobID          string `json:"jobId"`
		SaveCheckpoint bool   `json:"saveCheckpoint"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return "", false, err
	}
	return a.JobID, a.SaveCheckpoint, checkJobID(a.JobID)
}

// checkJobID is the rule every job operation applies to the job ID.
func checkJobID(id string) error {
	if id == "" || len(id) > maxRefBytes {
		return fmt.Errorf("%w: jobId is required", ErrBadRequest)
	}
	return nil
}
