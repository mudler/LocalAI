package backend

import "sync"

// TODO: dave-gray101 8/11: I think this is unused. Check back in a while to see if this should be removed, or refactored to _be_ used.

// mutex still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
var mutexMap sync.Mutex
var mutexes map[string]*sync.Mutex = make(map[string]*sync.Mutex)

func Lock(s string) *sync.Mutex {
	// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
	mutexMap.Lock()
	l, ok := mutexes[s]
	if !ok {
		m := &sync.Mutex{}
		mutexes[s] = m
		l = m
	}
	mutexMap.Unlock()
	l.Lock()

	return l
}
