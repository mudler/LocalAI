package openai

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
	"github.com/pion/ice/v4"
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
func webRTCSettingEngine(cfg *config.ApplicationConfig) (webrtc.SettingEngine, error) {
	s := webrtc.SettingEngine{}
	if cfg == nil {
		return s, nil
	}
	if len(cfg.WebRTCNAT1To1IPs) > 0 {
		s.SetNAT1To1IPs(cfg.WebRTCNAT1To1IPs, webrtc.ICECandidateTypeHost)
		xlog.Debug("realtime webrtc: advertising NAT 1:1 host IPs", "ips", cfg.WebRTCNAT1To1IPs)
	}
	if filter := iceInterfaceFilter(cfg.WebRTCICEInterfaces); filter != nil {
		s.SetInterfaceFilter(filter)
		xlog.Debug("realtime webrtc: restricting ICE interfaces", "interfaces", cfg.WebRTCICEInterfaces)
	}
	if cfg.WebRTCUDPPort > 0 {
		mux, err := udpMuxOnPort(cfg.WebRTCUDPPort, cfg.WebRTCICEInterfaces)
		if err != nil {
			return s, err
		}
		s.SetICEUDPMux(mux)
		xlog.Debug("realtime webrtc: sharing a fixed UDP port", "port", cfg.WebRTCUDPPort)
	}
	return s, nil
}

// udpMuxOnPort binds port on each interface the allow-list admits and returns a
// mux every session shares.
//
// One socket per interface address rather than one wildcard socket: for a mux
// bound to 0.0.0.0 pion derives the host candidates by enumerating interfaces
// itself, with no filter and loopback included, and its muxed gathering path
// never consults SetInterfaceFilter. A wildcard socket therefore silently
// discards WebRTCICEInterfaces and re-advertises exactly the docker0/veth
// addresses that setting exists to suppress.
func udpMuxOnPort(port int, interfaces []string) (ice.UDPMux, error) {
	// UDP4 only, matching the socket family this replaces.
	opts := []ice.UDPMuxFromPortOption{ice.UDPMuxFromPortWithNetworks(ice.NetworkTypeUDP4)}
	if filter := iceInterfaceFilter(interfaces); filter != nil {
		opts = append(opts, ice.UDPMuxFromPortWithInterfaceFilter(filter))
	}

	mux, err := ice.NewMultiUDPMuxFromPort(port, opts...)
	if err != nil {
		return nil, fmt.Errorf("bind WebRTC UDP port %d: %w", port, err)
	}
	// An empty mux binds nothing and would leave signaling working while no
	// candidate is ever advertised, so report the misconfiguration instead.
	if len(mux.GetListenAddresses()) == 0 {
		_ = mux.Close()
		if len(interfaces) > 0 {
			return nil, fmt.Errorf("bind WebRTC UDP port %d: no usable IPv4 address on interfaces %v", port, interfaces)
		}
		return nil, fmt.Errorf("bind WebRTC UDP port %d: no usable IPv4 address on this host", port)
	}
	return mux, nil
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
