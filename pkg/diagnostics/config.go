// SPDX-License-Identifier: MIT
package diagnostics

import (
	"fmt"
	"net"
	"strconv"
)

// Options configures independently opt-in profiling and request timing.
type Options struct {
	Pprof                bool
	Address              string
	MutexProfileFraction int
	BlockProfileRate     int
	RequestPhaseTiming   bool
}

func DefaultOptions() Options { return Options{Address: "127.0.0.1:6060"} }

// Validate checks even disabled settings so invalid configuration fails startup.
func (o Options) Validate() error {
	host, port, err := net.SplitHostPort(o.Address)
	if err != nil {
		return fmt.Errorf("invalid diagnostics address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("diagnostics address must use a numeric loopback IP")
	}
	if port == "" {
		return fmt.Errorf("diagnostics address requires a port")
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return fmt.Errorf("diagnostics port must be decimal")
		}
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return fmt.Errorf("diagnostics port must be in 1–65535")
	}
	if o.MutexProfileFraction < 0 || o.BlockProfileRate < 0 {
		return fmt.Errorf("diagnostics sampling rates must be nonnegative")
	}
	if !o.Pprof && (o.MutexProfileFraction != 0 || o.BlockProfileRate != 0) {
		return fmt.Errorf("diagnostics sampling requires pprof")
	}
	return nil
}
