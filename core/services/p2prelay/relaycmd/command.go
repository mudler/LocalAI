// Package relaycmd is the command line of a gated relay. `local-ai p2p relay`
// and the small `localai-relay` binary share it, so both take the same flags.
package relaycmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/services/p2prelay"
)

// Command runs a gated relay. A relay is gated: only the admin servers it
// names (or, with --open, anyone) can use it.
type Command struct {
	Admin                []string      `env:"LOCALAI_RELAY_ADMINS" help:"Peer ID of a server that administers this relay (repeatable, or comma separated). With no admin and no --open, the relay prints a one-time claim code and the first server that sends it becomes the admin"`
	DataDir              string        `name:"data-dir" aliases:"data" env:"LOCALAI_RELAY_DATA" type:"path" help:"Folder for the relay key and the allowlist (default: the user config folder, localai-relay)"`
	Port                 int           `env:"LOCALAI_RELAY_PORT" default:"4001" help:"Port for TCP and QUIC (IPv4 and IPv6)"`
	Listen               []string      `env:"LOCALAI_RELAY_LISTEN" help:"Listen multiaddr (repeatable); replaces --port"`
	PublicIP             string        `env:"LOCALAI_RELAY_PUBLIC_IP" help:"Announce this IPv4 address instead of the interface addresses. Needed on clouds with 1:1 NAT, where the host only has a private address"`
	Announce             []string      `env:"LOCALAI_RELAY_ANNOUNCE" help:"Announce exactly these multiaddrs (repeatable)"`
	Open                 bool          `env:"LOCALAI_RELAY_OPEN" help:"Run an open relay that anyone can use. Not recommended: it is logged as a warning at start and every 10 minutes, and it has a bandwidth limit of 64 KiB/s per circuit unless you change --rate-limit"`
	CircuitMaxBytes      int64         `default:"1073741824" help:"Data one circuit may carry in each direction, in bytes (0: no limit)"`
	CircuitMaxDuration   time.Duration `default:"30m" help:"Time one circuit may stay open (0: no limit)"`
	RateLimit            int64         `default:"-1" help:"Bandwidth limit per circuit, in bytes per second (0: no limit, with a warning; default: 64 KiB/s for an open relay, none otherwise)"`
	MaxCircuitBytes      int64         `default:"8589934592" help:"Ceiling for the data limit per circuit that an admin may set from a server, in bytes (0: no ceiling). The limit flags must stay inside it, and an admin cannot remove a limit that has a ceiling"`
	MaxCircuitDuration   time.Duration `default:"24h" help:"Ceiling for the time limit per circuit that an admin may set from a server (0: no ceiling)"`
	MaxRateLimit         int64         `default:"0" help:"Ceiling for the bandwidth limit per circuit that an admin may set from a server, in bytes per second (0: no ceiling)"`
	TotalRateLimit       int64         `default:"0" help:"Bandwidth limit for the whole relay, in bytes per second (0: no limit)"`
	MaxCircuits          int           `default:"128" help:"Circuits the relay carries at once"`
	MaxCircuitsPerPeer   int           `default:"16" help:"Circuits one peer may hold at once"`
	MaxReservations      int           `default:"128" help:"Reservations the relay holds at once"`
	MaxReservationsPerIP int           `default:"8" help:"Reservations one IP address may hold"`
	MaxConnsPerIP        int           `default:"32" help:"Connections one IP address may hold"`
	ConnRatePerIP        float64       `default:"2" help:"New connections per second one IP address may open after its burst (0: no limit). Loopback is never limited"`
	ConnBurstPerIP       int           `default:"128" help:"New connections one IP address may open at once. Many devices can share one address"`
	ConnRateTotal        float64       `default:"200" help:"New connections per second from all addresses together after the burst (0: no limit)"`
	ConnBurstTotal       int           `default:"1000" help:"New connections from all addresses together that the relay accepts at once"`
	MaxPeersPerAdmin     int           `default:"1024" help:"Peers one admin may allow"`
	PendingTTL           time.Duration `default:"10m" help:"How long a device that came in with a relay pass stays a member until its server confirms it"`
	AllowPrivateAddrs    bool          `help:"Put private addresses in reservations (for a relay on a LAN). Turns on by itself when no public address is known"`
	ResetAdmins          bool          `help:"Forget the stored admins and allowlists and start unclaimed"`
	JSON                 bool          `help:"Print the start-up banner as one JSON line, for scripts"`
	Healthcheck          bool          `help:"Do not start a relay: exit 0 when one listens on --port of this host, 1 otherwise. For container health checks"`
}

// Config maps the flags to the relay's configuration.
func (c *Command) Config() (p2prelay.RunConfig, error) {
	cfg := p2prelay.RunConfig{Config: p2prelay.DefaultConfig(c.Open)}
	var err error
	if cfg.Admins, err = p2prelay.ParseAdmins(c.Admin); err != nil {
		return cfg, err
	}
	if cfg.Announce, err = p2prelay.ParseAnnounce(c.Announce); err != nil {
		return cfg, err
	}
	if c.PublicIP != "" {
		if cfg.PublicIP, err = p2prelay.ParseIP(c.PublicIP); err != nil {
			return cfg, fmt.Errorf("--public-ip: %w", err)
		}
	}
	if c.Open && len(cfg.Admins) > 0 {
		return cfg, errors.New("--open and --admin exclude each other: an open relay serves everyone")
	}
	if c.DataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return cfg, fmt.Errorf("no --data-dir and no user config folder: %w", err)
		}
		c.DataDir = filepath.Join(base, "localai-relay")
	}
	cfg.DataDir = c.DataDir
	cfg.AllowPrivateAddrs = c.AllowPrivateAddrs
	cfg.CircuitMaxBytes = c.CircuitMaxBytes
	cfg.CircuitMaxDuration = c.CircuitMaxDuration
	if c.RateLimit >= 0 {
		// An explicit value wins, including 0, which removes the limit.
		cfg.RateLimit = c.RateLimit
	}
	cfg.Ceiling = p2prelay.Ceiling{
		CircuitMaxBytes: c.MaxCircuitBytes, CircuitMaxDuration: c.MaxCircuitDuration, RateLimit: c.MaxRateLimit,
	}
	cfg.TotalRateLimit = c.TotalRateLimit
	cfg.MaxCircuits = c.MaxCircuits
	cfg.MaxCircuitsPerPeer = c.MaxCircuitsPerPeer
	cfg.MaxReservations = c.MaxReservations
	cfg.MaxReservationsPerIP = c.MaxReservationsPerIP
	cfg.MaxConnsPerIP = c.MaxConnsPerIP
	cfg.ConnRatePerIP = c.ConnRatePerIP
	cfg.ConnBurstPerIP = c.ConnBurstPerIP
	cfg.ConnRateTotal = c.ConnRateTotal
	cfg.ConnBurstTotal = c.ConnBurstTotal
	cfg.MaxPeersPerAdmin = c.MaxPeersPerAdmin
	cfg.PendingTTL = c.PendingTTL
	cfg.Port = c.Port
	cfg.Listen = c.Listen
	cfg.ResetAdmins = c.ResetAdmins
	cfg.JSON = c.JSON
	return cfg, nil
}

// Execute runs the relay until ctx ends or the process gets SIGINT or SIGTERM.
// With --json, standard output carries only the JSON banner line. Execute does
// not change the process logger: the caller must send log lines to standard
// error in that case (see logsetup.SetLogger).
func (c *Command) Execute(ctx context.Context) error {
	if c.Healthcheck {
		return c.healthcheck()
	}
	cfg, err := c.Config()
	if err != nil {
		return err
	}
	return p2prelay.Run(ctx, cfg)
}

// healthcheck connects to the relay's TCP port on this host.
func (c *Command) healthcheck() error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port)), 3*time.Second)
	if err != nil {
		return fmt.Errorf("no relay answers on port %d: %w", c.Port, err)
	}
	return conn.Close()
}
