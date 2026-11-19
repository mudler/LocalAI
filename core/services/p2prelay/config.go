package p2prelay

import (
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

// Default limits. A gated relay has no bandwidth limit by default; an open
// relay is limited to DefaultOpenRateLimit per circuit and direction.
const (
	DefaultCircuitMaxBytes      int64 = 1 << 30
	DefaultCircuitMaxDuration         = 30 * time.Minute
	DefaultOpenRateLimit        int64 = 64 << 10
	DefaultMaxCircuits                = 128
	DefaultMaxCircuitsPerPeer         = 16
	DefaultMaxReservations            = 128
	DefaultMaxReservationsPerIP       = 8
	DefaultMaxConnsPerIP              = 32
	DefaultConnRatePerIP              = 2.0
	DefaultConnBurstPerIP             = 128
	DefaultConnRateTotal              = 200.0
	DefaultConnBurstTotal             = 1000
	DefaultMaxPeersPerAdmin           = 1024
	DefaultMaxPassesPerAdmin          = 64
	DefaultPendingTTL                 = 10 * time.Minute
	DefaultStrangerTimeout            = 10 * time.Second

	// DefaultCeilingCircuitMaxBytes and DefaultCeilingCircuitMaxDuration are
	// the most data and time per circuit that an admin may set at runtime:
	// eight times and 48 times the defaults. DefaultCeilingRateLimit is 0:
	// no ceiling on the bandwidth, as a gated relay has no bandwidth limit by
	// default.
	DefaultCeilingCircuitMaxBytes    int64 = 8 << 30
	DefaultCeilingCircuitMaxDuration       = 24 * time.Hour
	DefaultCeilingRateLimit          int64 = 0

	// MaxPassTTL is the longest lifetime of a relay pass.
	MaxPassTTL = 30 * 24 * time.Hour
)

// Limits are the circuit and reservation limits of a relay. For every field,
// zero means no limit; DefaultLimits gives the defaults.
type Limits struct {
	// CircuitMaxBytes is the data limit per circuit and direction. When the
	// limit is reached the relay closes the circuit.
	CircuitMaxBytes int64
	// CircuitMaxDuration is the lifetime of a circuit. When both
	// CircuitMaxBytes and CircuitMaxDuration are zero, circuits are not
	// "limited" and dialers need no limited-connection permission.
	CircuitMaxDuration time.Duration
	// RateLimit is the bandwidth of one circuit, per direction, in bytes per
	// second.
	RateLimit int64
	// TotalRateLimit is the bandwidth of all circuits together, both
	// directions counted, in bytes per second.
	TotalRateLimit int64
	// MaxCircuits is the number of open circuits on the whole relay.
	MaxCircuits int
	// MaxCircuitsPerPeer is the number of open circuits of one peer, as
	// source or destination.
	MaxCircuitsPerPeer int
	// MaxReservations is the number of reservations on the whole relay.
	MaxReservations int
	// MaxReservationsPerIP is the number of reservations from one IP address.
	MaxReservationsPerIP int
}

// DefaultLimits returns the default limits of a gated relay: 1 GiB and 30
// minutes per circuit, 128 circuits (16 per peer), 128 reservations (8 per
// IP) and no bandwidth limit.
func DefaultLimits() Limits {
	return Limits{
		CircuitMaxBytes:      DefaultCircuitMaxBytes,
		CircuitMaxDuration:   DefaultCircuitMaxDuration,
		MaxCircuits:          DefaultMaxCircuits,
		MaxCircuitsPerPeer:   DefaultMaxCircuitsPerPeer,
		MaxReservations:      DefaultMaxReservations,
		MaxReservationsPerIP: DefaultMaxReservationsPerIP,
	}
}

func (l Limits) validate() error {
	if l.CircuitMaxBytes < 0 || l.CircuitMaxDuration < 0 || l.RateLimit < 0 || l.TotalRateLimit < 0 ||
		l.MaxCircuits < 0 || l.MaxCircuitsPerPeer < 0 || l.MaxReservations < 0 || l.MaxReservationsPerIP < 0 {
		return errors.New("p2prelay: limits must not be negative")
	}
	return nil
}

// resources maps the limits to the circuit v2 relay resources.
func (l Limits) resources() relayv2.Resources {
	res := relayv2.DefaultResources()
	res.MaxReservations = orMax(l.MaxReservations)
	res.MaxReservationsPerIP = orMax(l.MaxReservationsPerIP)
	res.MaxCircuits = orMax(l.MaxCircuitsPerPeer)
	if l.CircuitMaxBytes == 0 && l.CircuitMaxDuration == 0 {
		res.Limit = nil
		return res
	}
	res.Limit = &relayv2.RelayLimit{Data: l.CircuitMaxBytes, Duration: l.CircuitMaxDuration}
	if res.Limit.Data == 0 {
		res.Limit.Data = math.MaxInt64
	}
	if res.Limit.Duration == 0 {
		// The relay always sets a deadline on a limited circuit.
		res.Limit.Duration = 100 * 365 * 24 * time.Hour
	}
	return res
}

func orMax(n int) int {
	if n == 0 {
		return math.MaxInt32
	}
	return n
}

// Ceiling is the highest value an admin may set for each per-circuit limit
// with the limits admin operation. The operator sets it; an admin cannot
// change it. For every field, zero means no ceiling. With a ceiling, an admin
// cannot remove the limit either, because "no limit" is above any ceiling.
type Ceiling struct {
	// CircuitMaxBytes is the highest data limit per circuit and direction.
	CircuitMaxBytes int64
	// CircuitMaxDuration is the longest time limit per circuit.
	CircuitMaxDuration time.Duration
	// RateLimit is the highest bandwidth limit per circuit and direction, in
	// bytes per second.
	RateLimit int64
}

// DefaultCeiling returns the default ceiling.
func DefaultCeiling() Ceiling {
	return Ceiling{
		CircuitMaxBytes:    DefaultCeilingCircuitMaxBytes,
		CircuitMaxDuration: DefaultCeilingCircuitMaxDuration,
		RateLimit:          DefaultCeilingRateLimit,
	}
}

// Config configures a gated relay. Start from DefaultConfig: for the limits
// in Limits and for MaxConnsPerIP, zero means no limit.
type Config struct {
	// DataDir holds allowlist.json (the admin sets, the claimed admins and
	// the relay passes). It is created with mode 0700 if missing.
	DataDir string
	// Admins are the peer IDs of the LocalAI servers that manage the relay.
	// With no admin and Open false, the relay is unclaimed and prints a
	// claim code (see Relay.ClaimCode).
	Admins []peer.ID
	// Open makes the relay serve every peer. Use only for tests and trusted
	// networks; Status reports mode "open".
	Open bool
	// Announce replaces the addresses the host announces. Empty: the host's
	// own addresses, with PublicIP applied.
	Announce []ma.Multiaddr
	// PublicIP replaces the IPv4 address in the announced addresses. It is
	// needed on hosts behind a 1:1 NAT, where the interface has only a
	// private address.
	PublicIP net.IP
	// AllowPrivateAddrs puts private addresses in reservations (a LAN relay).
	// It turns on by itself when the host has no public address.
	AllowPrivateAddrs bool

	Limits
	// Ceiling bounds what an admin may set at runtime. The limits in Limits
	// (the flags) and a saved limits.json must stay inside it.
	Ceiling Ceiling
	// MaxConnsPerIP is the number of connections from one IPv4 address or
	// one IPv6 /56. Many devices can share one home or CGNAT address.
	MaxConnsPerIP int
	// ConnRatePerIP is the number of new connections per second that one
	// IPv4 address (or IPv6 /56) may open once its burst is used up, and
	// ConnBurstPerIP is the burst. Many devices behind one home, office or
	// CGNAT address redeem invites and reconnect together, so the burst is
	// large. Zero for the rate means no limit. Loopback is never limited.
	ConnRatePerIP  float64
	ConnBurstPerIP int
	// ConnRateTotal and ConnBurstTotal are the same for all addresses
	// together: the ceiling for a flood from many addresses. Zero for the
	// rate means no limit.
	ConnRateTotal  float64
	ConnBurstTotal int
	// MaxPeersPerAdmin bounds the set of one admin, pending members included.
	MaxPeersPerAdmin int
	// MaxPassesPerAdmin bounds the open relay passes of one admin.
	MaxPassesPerAdmin int
	// PendingTTL is how long a peer admitted by a relay pass stays a member
	// unless its admin confirms it with add or replace.
	PendingTTL time.Duration
	// StrangerTimeout closes the connection of a peer that is not a member
	// (it may only present a pass or claim the relay) after this time.
	StrangerTimeout time.Duration
}

// DefaultConfig returns the defaults for a gated relay (open false) or an
// open relay (open true). They differ only in the bandwidth limit: an open
// relay gets DefaultOpenRateLimit per circuit, a gated one none.
func DefaultConfig(open bool) Config {
	c := Config{
		Open:              open,
		Limits:            DefaultLimits(),
		Ceiling:           DefaultCeiling(),
		MaxConnsPerIP:     DefaultMaxConnsPerIP,
		ConnRatePerIP:     DefaultConnRatePerIP,
		ConnBurstPerIP:    DefaultConnBurstPerIP,
		ConnRateTotal:     DefaultConnRateTotal,
		ConnBurstTotal:    DefaultConnBurstTotal,
		MaxPeersPerAdmin:  DefaultMaxPeersPerAdmin,
		MaxPassesPerAdmin: DefaultMaxPassesPerAdmin,
		PendingTTL:        DefaultPendingTTL,
		StrangerTimeout:   DefaultStrangerTimeout,
	}
	if open {
		c.RateLimit = DefaultOpenRateLimit
	}
	return c
}

func (c *Config) validate() error {
	if c.DataDir == "" {
		return errors.New("p2prelay: DataDir is required")
	}
	if err := c.Limits.validate(); err != nil {
		return err
	}
	if err := c.Ceiling.validate(); err != nil {
		return err
	}
	if err := c.Ceiling.checkStart(c.Limits); err != nil {
		return err
	}
	if c.MaxConnsPerIP < 0 {
		return errors.New("p2prelay: MaxConnsPerIP must not be negative")
	}
	if c.ConnRatePerIP < 0 || c.ConnRateTotal < 0 || c.ConnBurstPerIP < 0 || c.ConnBurstTotal < 0 {
		return errors.New("p2prelay: connection rates and bursts must not be negative")
	}
	if (c.ConnRatePerIP > 0 && c.ConnBurstPerIP == 0) || (c.ConnRateTotal > 0 && c.ConnBurstTotal == 0) {
		return errors.New("p2prelay: a connection rate needs a burst of at least 1")
	}
	if c.MaxPeersPerAdmin <= 0 || c.MaxPassesPerAdmin <= 0 {
		return errors.New("p2prelay: MaxPeersPerAdmin and MaxPassesPerAdmin must be positive")
	}
	if c.PendingTTL <= 0 || c.StrangerTimeout <= 0 {
		return errors.New("p2prelay: PendingTTL and StrangerTimeout must be positive")
	}
	if c.PublicIP != nil && c.PublicIP.To4() == nil {
		return fmt.Errorf("p2prelay: PublicIP %s is not an IPv4 address", c.PublicIP)
	}
	return nil
}
