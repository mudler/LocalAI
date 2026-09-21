package monitoring

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// LocalProcessSampler reads the resource use of backend processes running on
// this host.
//
// It keeps one gopsutil handle per PID between calls because a process's CPU
// share is a delta between two readings. A fresh handle on every request can
// only report the lifetime average, which for a model loaded hours ago says
// nothing about what it is doing now.
type LocalProcessSampler struct {
	mu    sync.Mutex
	procs map[int32]*gopsutil.Process
}

func NewLocalProcessSampler() *LocalProcessSampler {
	return &LocalProcessSampler{procs: map[int32]*gopsutil.Process{}}
}

// Sample reads memory, CPU and start time for pid. CPUPercent stays nil on the
// first reading of a PID: there is no earlier sample to take a delta against,
// and reporting 0 would read as "idle" rather than "not measured yet".
func (s *LocalProcessSampler) Sample(pid int32) (*schema.SysInfoProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proc, seen := s.procs[pid]
	if !seen {
		var err error
		proc, err = gopsutil.NewProcess(pid)
		if err != nil {
			return nil, err
		}
		s.procs[pid] = proc
	}

	mem, err := proc.MemoryInfo()
	if err != nil {
		delete(s.procs, pid)
		return nil, err
	}

	out := &schema.SysInfoProcess{PID: pid, RSSBytes: mem.RSS}

	if pct, err := proc.MemoryPercent(); err == nil {
		out.MemoryPercent = pct
	}
	if created, err := proc.CreateTime(); err == nil {
		out.StartedAt = time.UnixMilli(created).UTC()
	}
	// Percent(0) measures against the previous call on this handle and
	// returns 0 on the first one, which is exactly the case to leave unset.
	if cpu, err := proc.Percent(0); err == nil && seen {
		out.CPUPercent = &cpu
	}
	return out, nil
}

// Retain forgets every PID not in live, so handles for unloaded models do not
// accumulate and a recycled PID starts from a clean reading.
func (s *LocalProcessSampler) Retain(live map[int32]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for pid := range s.procs {
		if _, ok := live[pid]; !ok {
			delete(s.procs, pid)
		}
	}
}
