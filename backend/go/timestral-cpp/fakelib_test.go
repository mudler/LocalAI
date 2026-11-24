package main

import (
	"math"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// fakeLib replaces the package-level Cpp* function variables so the backend can
// be tested without libtimestral.so. Forecast output encodes where each value
// came from: see value().
type fakeLib struct {
	mu sync.Mutex

	levels       []float64
	abiVersion   int32
	loadFails    bool
	forecastFail bool
	delay        time.Duration

	loads, frees, freeBuffers int
	lastErr                   string
	calls                     []fakeCall
	mapped                    map[uintptr][]byte // buffers handed out, outside the Go heap like malloc'd C memory

	inFlight, maxInFlight atomic.Int32
}

type fakeCall struct {
	B, V, T, H, cPast, cKnown, ccPast, ccKnown int32
	sym, clip, maxContext                      int32
	threads                                    int32
	context                                    []float32
	knownCov                                   []float32
	catPast                                    []float64
}

// value is the forecast value for batch row b, variate v, step h, quantile q:
// the first context value of that row and variate, then the position.
func value(first float32, h, q int) float32 { return first*1000 + float32(h*10+q) }

func (f *fakeLib) install() {
	CppAbiVersion = func() int32 { return f.abiVersion }
	CppLoad = func(path string, threads int32) uintptr {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.loadFails {
			f.lastErr = "cannot read model"
			return 0
		}
		f.loads++
		f.calls = append(f.calls, fakeCall{threads: threads})
		return 0x1000
	}
	CppFree = func(uintptr) { f.mu.Lock(); f.frees++; f.mu.Unlock() }
	CppLastError = func(uintptr) string { return f.lastErr }
	CppNumQuantiles = func(uintptr) int32 { return int32(len(f.levels)) }
	CppQuantileLevel = func(_ uintptr, i int32) float64 { return f.levels[i] }
	CppFreeBuffer = func(p uintptr) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.freeBuffers++
		if m, ok := f.mapped[p]; ok {
			_ = syscall.Munmap(m)
			delete(f.mapped, p)
		}
	}
	CppForecast = func(_ uintptr, req unsafe.Pointer, outQ unsafe.Pointer) uintptr {
		cur := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			prev := f.maxInFlight.Load()
			if cur <= prev || f.maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		r := (*forecastRequest)(req)
		call := fakeCall{
			B: r.B, V: r.V, T: r.T, H: r.predictionLen,
			cPast: r.cPast, cKnown: r.cKnown, ccPast: r.ccPast, ccKnown: r.ccKnown,
			sym: r.symmetricAvg, clip: r.clipNonneg, maxContext: r.maxContext,
		}
		ctx := unsafe.Slice((*float32)(r.context), int(r.B*r.V*r.T))
		call.context = append([]float32(nil), ctx...)
		if r.knownCov != nil {
			call.knownCov = append([]float32(nil), unsafe.Slice((*float32)(r.knownCov), int(r.B*r.cKnown*(r.T+r.predictionLen)))...)
		}
		if r.catPast != nil {
			call.catPast = append([]float64(nil), unsafe.Slice((*float64)(r.catPast), int(r.B*r.ccPast*r.T))...)
		}

		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, call)
		if f.forecastFail {
			f.lastErr = "invalid request"
			return 0
		}
		q := len(f.levels)
		out := make([]float32, 0, int(r.B*r.V*r.predictionLen)*q)
		for b := int32(0); b < r.B; b++ {
			for v := int32(0); v < r.V; v++ {
				first := ctx[(b*r.V+v)*r.T]
				if math.IsNaN(float64(first)) {
					first = 0
				}
				for h := 0; h < int(r.predictionLen); h++ {
					for qi := 0; qi < q; qi++ {
						out = append(out, value(first, h, qi))
					}
				}
			}
		}
		// Hand the output over in memory the Go runtime does not own, like the
		// malloc'd buffer of the real library.
		mem, err := syscall.Mmap(-1, 0, len(out)*4, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
		if err != nil {
			panic(err)
		}
		copy(unsafe.Slice((*float32)(unsafe.Pointer(&mem[0])), len(out)), out)
		p := uintptr(unsafe.Pointer(&mem[0]))
		if f.mapped == nil {
			f.mapped = map[uintptr][]byte{}
		}
		f.mapped[p] = mem
		*(*int32)(outQ) = int32(q)
		return p
	}
}

// forecastCalls returns the recorded Forecast calls, without the load records.
func (f *fakeLib) forecastCalls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.B > 0 {
			out = append(out, c)
		}
	}
	return out
}
