// SPDX-License-Identifier: MIT
package workerstatus

import "fmt"

// CapabilityCommand has no connection flags and never starts a worker.
type CapabilityCommand struct{}

func (*CapabilityCommand) Run() error { _, err := fmt.Println(Capability); return err }
