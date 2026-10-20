//go:build !linux

// SPDX-License-Identifier: MIT
package runtime

import "time"

// Other platforms fail closed until an exact durable process identity and
// handle-based orphan recovery adapter has been implemented and tested.
type processIdentity struct {
	PID                      int
	Boot, Start, Incarnation string
	Pending                  bool
}
type processLedger struct{}

func identity(int) (processIdentity, error)                    { return processIdentity{}, ErrRuntime }
func openLedger(string, time.Duration) (*processLedger, error) { return nil, ErrRuntime }
func (*processLedger) put(string, processIdentity) error       { return ErrRuntime }
func (*processLedger) remove(string, string) error             { return ErrRuntime }
func (*processLedger) close() error                            { return nil }
