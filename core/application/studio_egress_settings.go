package application

import (
	"sync"

	"github.com/mudler/xlog"
)

// lastGood parses one list setting of the Sandbox network and keeps the
// last value that parsed. The settings endpoint and the runtime settings
// file refuse a bad value, so one reaches here only by another way (a value
// a newer version reads more strictly, a hand edit read at startup). It
// must not change what commands may reach without a word: a bad value keeps
// the last good list in force and is logged once. A process that never saw
// a good value uses the fallback, the setting's default. The memory is per
// process: the value itself is the shared runtime setting.
type lastGood[T any] struct {
	name     string
	parse    func(string) (T, error)
	fallback T

	mu     sync.Mutex
	raw    string
	val    T
	ok     bool   // val holds a parsed value
	logged string // the bad value logged last
}

func newLastGood[T any](name string, parse func(string) (T, error), fallback T) *lastGood[T] {
	return &lastGood[T]{name: name, parse: parse, fallback: fallback}
}

// get returns the list for raw: parsed, or the last good one.
func (l *lastGood[T]) get(raw string) T {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ok && raw == l.raw {
		return l.val
	}
	v, err := l.parse(raw)
	if err == nil {
		l.raw, l.val, l.ok = raw, v, true
		return v
	}
	keep := l.fallback
	if l.ok {
		keep = l.val
	}
	if l.logged != raw {
		l.logged = raw
		xlog.Warn("Chats: a Sandbox network setting is not valid; the last valid value stays in force", "setting", l.name, "kept_last_valid", l.ok, "error", err)
	}
	return keep
}
