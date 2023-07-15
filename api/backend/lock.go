package backend

import "sync"

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
