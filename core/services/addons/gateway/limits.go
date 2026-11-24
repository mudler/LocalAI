// SPDX-License-Identifier: MIT
package gateway

import "time"

// RelayLimits are startup-only. Zero selects defaults only for the whole value;
// partially specified or unbounded overrides fail closed.
type RelayLimits struct {
	Bytes, ProgressBytes, AssetBytes  int64
	Concurrency                       int
	Deadline, PollInterval, Heartbeat time.Duration
}

func DefaultRelayLimits() RelayLimits {
	return RelayLimits{Bytes: 64 << 20, ProgressBytes: 1 << 20, AssetBytes: 8 << 20, Concurrency: 16, Deadline: 60 * time.Second, PollInterval: 250 * time.Millisecond, Heartbeat: 5 * time.Second}
}
func (l RelayLimits) Validate() error {
	if l.Bytes < 1 || l.Bytes > 64<<20 || l.ProgressBytes < 1 || l.ProgressBytes > 1<<20 || l.AssetBytes < 1 || l.AssetBytes > 8<<20 || l.Concurrency < 1 || l.Concurrency > 128 || l.Deadline < time.Millisecond || l.Deadline > 60*time.Second || l.PollInterval < 250*time.Millisecond || l.PollInterval > 5*time.Second || l.Heartbeat < l.PollInterval || l.Heartbeat > 10*time.Second {
		return ErrForbidden
	}
	return nil
}
func (l RelayLimits) resolved() (RelayLimits, error) {
	if l == (RelayLimits{}) {
		l = DefaultRelayLimits()
	}
	return l, l.Validate()
}
