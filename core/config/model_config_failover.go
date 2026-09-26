package config

import (
	"fmt"
	"time"
)

// FailoverConfig turns a model config into a failover chain: requests for the
// chain name are served by its highest-priority healthy target. See
// core/services/failover for the runtime side.
type FailoverConfig struct {
	Targets  []FailoverTarget `yaml:"targets" json:"targets"`
	Probe    FailoverProbe    `yaml:"probe,omitempty" json:"probe,omitempty"`
	Trip     FailoverTrip     `yaml:"trip,omitempty" json:"trip,omitempty"`
	Recovery FailoverRecovery `yaml:"recovery,omitempty" json:"recovery,omitempty"`
}

type FailoverTarget struct {
	Model string `yaml:"model" json:"model"`
	// Warm keeps a local target loaded and exempt from eviction, so a switch
	// does not wait for a cold load.
	Warm bool `yaml:"warm,omitempty" json:"warm,omitempty"`
}

type FailoverProbe struct {
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout  string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type FailoverTrip struct {
	Errors int    `yaml:"errors,omitempty" json:"errors,omitempty"`
	Window string `yaml:"window,omitempty" json:"window,omitempty"`
}

type FailoverRecovery struct {
	Probes   int    `yaml:"probes,omitempty" json:"probes,omitempty"`
	MinDwell string `yaml:"min_dwell,omitempty" json:"min_dwell,omitempty"`
}

const (
	DefaultFailoverProbeInterval  = 15 * time.Second
	DefaultFailoverProbeTimeout   = 5 * time.Second
	DefaultFailoverTripErrors     = 1
	DefaultFailoverTripWindow     = 30 * time.Second
	DefaultFailoverRecoveryProbes = 3
	DefaultFailoverMinDwell       = 60 * time.Second
)

// IsFailover reports whether this config is a failover chain.
func (c ModelConfig) IsFailover() bool { return c.Failover != nil }

func (f FailoverConfig) ProbeInterval() time.Duration {
	return durationOr(f.Probe.Interval, DefaultFailoverProbeInterval)
}
func (f FailoverConfig) ProbeTimeout() time.Duration {
	return durationOr(f.Probe.Timeout, DefaultFailoverProbeTimeout)
}
func (f FailoverConfig) TripWindow() time.Duration {
	return durationOr(f.Trip.Window, DefaultFailoverTripWindow)
}
func (f FailoverConfig) MinDwell() time.Duration {
	return durationOr(f.Recovery.MinDwell, DefaultFailoverMinDwell)
}
func (f FailoverConfig) TripErrors() int {
	if f.Trip.Errors <= 0 {
		return DefaultFailoverTripErrors
	}
	return f.Trip.Errors
}
func (f FailoverConfig) RecoveryProbes() int {
	if f.Recovery.Probes <= 0 {
		return DefaultFailoverRecoveryProbes
	}
	return f.Recovery.Probes
}

// WarmFailoverTargets returns the targets marked warm, in chain order.
func (c ModelConfig) WarmFailoverTargets() []string {
	if c.Failover == nil {
		return nil
	}
	var out []string
	for _, t := range c.Failover.Targets {
		if t.Warm {
			out = append(out, t.Model)
		}
	}
	return out
}

func durationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// validateFailover checks what a chain can check without other configs.
// Target existence is checked by ModelConfigLoader.ValidateFailoverTargets.
func (c *ModelConfig) validateFailover() error {
	if c.Name == "" {
		return fmt.Errorf("failover config requires a name")
	}
	if c.IsAlias() {
		return fmt.Errorf("model %q cannot set both alias and failover", c.Name)
	}
	if c.Backend != "" || c.Model != "" {
		return fmt.Errorf("failover config %q must not set backend or parameters.model: a chain is a pure redirect", c.Name)
	}
	f := c.Failover
	if len(f.Targets) < 2 {
		return fmt.Errorf("failover chain %q needs at least 2 targets", c.Name)
	}
	seen := map[string]bool{}
	for _, t := range f.Targets {
		switch {
		case t.Model == "":
			return fmt.Errorf("failover chain %q has a target with no model", c.Name)
		case t.Model == c.Name:
			return fmt.Errorf("failover chain %q cannot list itself", c.Name)
		case seen[t.Model]:
			return fmt.Errorf("failover chain %q lists %q twice", c.Name, t.Model)
		}
		seen[t.Model] = true
	}
	for key, v := range map[string]string{
		"probe.interval":     f.Probe.Interval,
		"probe.timeout":      f.Probe.Timeout,
		"trip.window":        f.Trip.Window,
		"recovery.min_dwell": f.Recovery.MinDwell,
	} {
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return fmt.Errorf("failover chain %q: invalid %s %q", c.Name, key, v)
		}
	}
	if f.Trip.Errors < 0 {
		return fmt.Errorf("failover chain %q: trip.errors must not be negative", c.Name)
	}
	if f.Recovery.Probes < 0 {
		return fmt.Errorf("failover chain %q: recovery.probes must not be negative", c.Name)
	}
	return nil
}
