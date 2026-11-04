// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/pkg/egresspolicy"
)

// NetworkKindEgressCheck is the one kind of network policy today: the add-on
// runs commands behind a proxy that asks the host about each connection with
// the host operation egress.check, and the host answers from the
// administrator's settings and the chat's rules.
const NetworkKindEgressCheck = "egress-check"

// egressCheckOp is the host operation of NetworkKindEgressCheck.
const egressCheckOp = "egress.check"

// Limits of the text the host shows for a network.
const (
	MaxNetworkTitleChars       = 40
	MaxNetworkPresetTitleChars = 40
	// MaxNetworkPresets bounds the presets an add-on may retitle: the host
	// knows three.
	MaxNetworkPresets = 3
)

// NetworkCapability is what an add-on declares about the network of the
// commands it runs. It carries no authority: the host enforces the
// administrator's ceiling and the chat's rules, and answers egress.check only
// for an add-on whose host setting grants that operation. The declaration
// attaches the network-policy host control to the add-on and names the
// network in the text the host shows.
type NetworkCapability struct {
	// Kind is NetworkKindEgressCheck.
	Kind string `json:"kind"`
	// Title names the network in the text the host shows the person and the
	// model, for example "Sandbox". It falls back to the add-on's title.
	Title string `json:"title,omitempty"`
	// Presets retitle the host's presets (off, registries, all) in the chat
	// Tools menu. A preset that is not listed keeps the host's title.
	Presets []NetworkPreset `json:"presets,omitempty"`
}

// NetworkPreset is the title an add-on gives one network preset.
type NetworkPreset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// validate checks the declaration against the other capabilities of the
// manifest. A nil declaration is valid.
func (n *NetworkCapability) validate(m Manifest) error {
	if n == nil {
		return nil
	}
	if n.Kind != NetworkKindEgressCheck {
		return fmt.Errorf("unknown network kind")
	}
	if ops := m.Capabilities.HostOperations; ops == nil || !(slices.Contains(ops.Required, egressCheckOp) || slices.Contains(ops.Optional, egressCheckOp)) {
		return fmt.Errorf("a network capability of kind %s needs the host operation %s", NetworkKindEgressCheck, egressCheckOp)
	}
	if n.Title != "" && !plainText(n.Title, MaxNetworkTitleChars) {
		return fmt.Errorf("invalid network title")
	}
	if len(n.Presets) > MaxNetworkPresets {
		return fmt.Errorf("too many network presets")
	}
	seen := map[string]bool{}
	for _, p := range n.Presets {
		if p.ID == "" || !egresspolicy.ValidEgressPreset(p.ID) || seen[p.ID] || !plainText(p.Title, MaxNetworkPresetTitleChars) {
			return fmt.Errorf("invalid or repeated network preset")
		}
		seen[p.ID] = true
	}
	return nil
}

// PresetTitle returns the title the add-on gives preset id, or fallback (the
// host's own title) when it gives none. It accepts a nil receiver.
func (n *NetworkCapability) PresetTitle(id, fallback string) string {
	if n != nil {
		for _, p := range n.Presets {
			if p.ID == id {
				return p.Title
			}
		}
	}
	return fallback
}
