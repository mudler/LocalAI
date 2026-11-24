// SPDX-License-Identifier: MIT
package motion

import (
	"math"
	"time"
)

const (
	UploadWindow        = 8
	UploadMaxBytes      = 8 << 20
	UploadFrameMaxBytes = 2 << 20
	UploadMaxQueueAge   = 250 * time.Millisecond
)

// QueuedFrame owns its encoded Input until inference starts or it is discarded.
type QueuedFrame struct {
	Sequence   uint64
	SourceTime int64
	Data       []byte
	Arrived    time.Time
}

// FrameQueue has one external lock/owner. It never owns the active inference.
type FrameQueue struct {
	frames  []QueuedFrame
	bytes   int
	Dropped uint64
}

func (q *FrameQueue) OldestAge(now time.Time) time.Duration {
	if len(q.frames) == 0 {
		return 0
	}
	return max(0, now.Sub(q.frames[0].Arrived))
}

func (q *FrameQueue) Len() int   { return len(q.frames) }
func (q *FrameQueue) Bytes() int { return q.bytes }

// Thin uniformly samples the entire pending span, including both endpoints.
// It releases removed buffers immediately rather than retaining slice capacity.
func (q *FrameQueue) thin(keep int) []uint64 {
	survivors := make([]QueuedFrame, 0, keep)
	released := make([]uint64, 0, len(q.frames)-keep)
	selected := make(map[int]bool, keep)
	if keep == 1 {
		selected[len(q.frames)-1] = true
	} else if keep > 1 {
		selected[0], selected[len(q.frames)-1] = true, true
		first, last := q.frames[0].SourceTime, q.frames[len(q.frames)-1].SourceTime
		previous := 0
		for sample := 1; sample < keep-1; sample++ {
			target := float64(first) + float64(last-first)*float64(sample)/float64(keep-1)
			nearest := previous + 1
			for candidate := nearest + 1; candidate <= len(q.frames)-(keep-sample); candidate++ {
				if math.Abs(float64(q.frames[candidate].SourceTime)-target) < math.Abs(float64(q.frames[nearest].SourceTime)-target) {
					nearest = candidate
				}
			}
			selected[nearest] = true
			previous = nearest
		}
	}
	bytes := 0
	for i, frame := range q.frames {
		if selected[i] {
			survivors = append(survivors, frame)
			bytes += len(frame.Data)
		} else {
			released = append(released, frame.Sequence)
		}
	}
	q.frames, q.bytes = survivors, bytes
	q.Dropped += uint64(len(released))
	return released
}

func (q *FrameQueue) Expire(now time.Time) []uint64 {
	var released []uint64
	for len(q.frames) > 0 && now.Sub(q.frames[0].Arrived) >= UploadMaxQueueAge {
		f := q.frames[0]
		released = append(released, f.Sequence)
		q.bytes -= len(f.Data)
		q.frames[0] = QueuedFrame{}
		q.frames = q.frames[1:]
	}
	q.Dropped += uint64(len(released))
	return released
}

// Offer accepts one bounded frame, then thins before another read can grow the queue.
func (q *FrameQueue) Offer(frame QueuedFrame) (released []uint64, pressure bool) {
	released = q.Expire(frame.Arrived)
	q.frames = append(q.frames, frame)
	q.bytes += len(frame.Data)
	pressure = len(q.frames) >= 6 || q.bytes >= 6<<20
	if pressure {
		released = append(released, q.thin(max(1, len(q.frames)/2))...)
	}
	return released, pressure
}

func (q *FrameQueue) Take() (QueuedFrame, bool) {
	if len(q.frames) == 0 {
		return QueuedFrame{}, false
	}
	f := q.frames[0]
	q.frames[0] = QueuedFrame{}
	q.frames = q.frames[1:]
	q.bytes -= len(f.Data)
	return f, true
}
