package tunnel

import "github.com/libp2p/go-yamux/v5"

// SessionConfigFor exposes the yamux configuration of a lane to the specs.
func SessionConfigFor(lane Lane) *yamux.Config { return sessionConfig(lane) }
