package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
	"github.com/pion/webrtc/v4"
)

// webRTCSettingEngine builds the pion SettingEngine for /v1/realtime WebRTC.
//
// With a default (empty) SettingEngine, pion gathers a host ICE candidate for
// every local interface. Under Docker host networking that includes bridge
// addresses (docker0/veth, 172.x) that a remote browser cannot route to; the
// connection often establishes on a good pair and then drops once ICE consent
// checks fail on the unreachable ones. The two opt-in knobs below let an
// operator advertise only the reachable address.
func webRTCSettingEngine(cfg *config.ApplicationConfig) webrtc.SettingEngine {
	s := webrtc.SettingEngine{}
	if cfg == nil {
		return s
	}
	if len(cfg.WebRTCNAT1To1IPs) > 0 {
		s.SetNAT1To1IPs(cfg.WebRTCNAT1To1IPs, webrtc.ICECandidateTypeHost)
		xlog.Debug("realtime webrtc: advertising NAT 1:1 host IPs", "ips", cfg.WebRTCNAT1To1IPs)
	}
	if filter := iceInterfaceFilter(cfg.WebRTCICEInterfaces); filter != nil {
		s.SetInterfaceFilter(filter)
		xlog.Debug("realtime webrtc: restricting ICE interfaces", "interfaces", cfg.WebRTCICEInterfaces)
	}
	return s
}

// iceInterfaceFilter returns an interface allow-list predicate for pion, or nil
// when no interfaces are configured (pion's default: gather from all).
func iceInterfaceFilter(allowed []string) func(string) bool {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	return func(iface string) bool {
		_, ok := set[iface]
		return ok
	}
}
