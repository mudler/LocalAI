package main

import "sync/atomic"

type atomicBool struct{ v atomic.Bool }

func (b *atomicBool) Set(x bool) { b.v.Store(x) }
func (b *atomicBool) Get() bool  { return b.v.Load() }

// jsonRaw lets a pre-encoded JSON string be embedded without re-quoting.
type jsonRaw string

func (j jsonRaw) MarshalJSON() ([]byte, error) { return []byte(j), nil }
