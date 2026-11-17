package cli

import (
	"fmt"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

// P2PCMD is the `local-ai p2p` command group: the gated relay, the relays a
// server uses, and the helpers for pairing and forwarding. Every command takes
// its flags here, so the bodies can be filled in without changing the command
// tree. A command that is not implemented yet fails with errNotImplemented.
type P2PCMD struct {
	Relay   P2PRelayCMD   `cmd:"" help:"Run a gated relay: a small public node that carries the traffic of your paired servers and devices when they cannot reach each other directly"`
	Relays  P2PRelaysCMD  `cmd:"" help:"Manage the relays this server uses (add, remove, list, check)"`
	ID      P2PIDCMD      `cmd:"" name:"id" help:"Show the peer ID and addresses of this server"`
	Status  P2PStatusCMD  `cmd:"" help:"Show the P2P state of this server: mode, reachability and the path to each peer"`
	Pair    P2PPairCMD    `cmd:"" help:"Redeem an invite link on this machine and pair it with a server"`
	Forward P2PForwardCMD `cmd:"" help:"Forward a local port to a service of a paired peer"`
}

func errNotImplemented(command string) error {
	return fmt.Errorf("%s is not available yet in this build", command)
}

// P2PRelayCMD runs a gated relay. A relay is gated: only the admin servers it
// names (or, with --open, anyone) can use it.
type P2PRelayCMD struct {
	Admin                []string      `env:"LOCALAI_RELAY_ADMINS" help:"Peer ID of a server that administers this relay (repeatable). With no admin and no --open, the relay prints a one-time claim code and the first server that sends it becomes the admin"`
	Data                 string        `env:"LOCALAI_RELAY_DATA" type:"path" help:"Folder for the relay key and the allowlist (default: the user config folder, localai-relay)"`
	Port                 int           `env:"LOCALAI_RELAY_PORT" default:"4001" help:"Port for TCP and QUIC (IPv4 and IPv6)"`
	Listen               []string      `env:"LOCALAI_RELAY_LISTEN" help:"Listen multiaddr (repeatable); replaces --port"`
	PublicIP             string        `env:"LOCALAI_RELAY_PUBLIC_IP" help:"Announce this IPv4 address instead of the interface addresses"`
	Announce             []string      `env:"LOCALAI_RELAY_ANNOUNCE" help:"Announce exactly these multiaddrs (repeatable)"`
	Open                 bool          `env:"LOCALAI_RELAY_OPEN" help:"Run an open relay that anyone can use. Not recommended: it is logged as a warning at start and every 10 minutes"`
	CircuitMaxBytes      int64         `default:"1073741824" help:"Data one circuit may carry in each direction, in bytes (0: no limit)"`
	CircuitMaxDuration   time.Duration `default:"30m" help:"Time one circuit may stay open (0: no limit)"`
	RateLimit            int64         `default:"-1" help:"Bandwidth limit per circuit, in bytes per second (0: no limit; default: 64 KiB/s for an open relay, none otherwise)"`
	TotalRateLimit       int64         `default:"0" help:"Bandwidth limit for the whole relay, in bytes per second (0: no limit)"`
	MaxCircuits          int           `default:"128" help:"Circuits the relay carries at once"`
	MaxCircuitsPerPeer   int           `default:"16" help:"Circuits one peer may hold at once"`
	MaxReservations      int           `default:"128" help:"Reservations the relay holds at once"`
	MaxReservationsPerIP int           `default:"8" help:"Reservations one IP address may hold"`
	MaxConnsPerIP        int           `default:"32" help:"Connections one IP address may hold"`
	MaxPeersPerAdmin     int           `default:"1024" help:"Peers one admin may allow"`
	AllowPrivateAddrs    bool          `help:"Put private addresses in reservations (for a relay on a LAN). Turns on by itself when no public address is known"`
	ResetAdmins          bool          `help:"Forget the stored admins and allowlists and start unclaimed"`
}

func (c *P2PRelayCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p relay")
}

// P2PServerFlags say which running server the server-side commands talk to.
type P2PServerFlags struct {
	Target string `env:"LOCALAI_P2P_TARGET" default:"http://localhost:8080" help:"Base URL of the LocalAI server"`
	APIKey string `env:"LOCALAI_API_KEY" help:"API key of an admin of the server"`
	JSON   bool   `help:"Print JSON"`
}

// P2PRelaysCMD manages the relays of a running server.
type P2PRelaysCMD struct {
	Add    P2PRelaysAddCMD    `cmd:"" help:"Add a relay to this server"`
	Remove P2PRelaysRemoveCMD `cmd:"" help:"Remove a relay from this server"`
	List   P2PRelaysListCMD   `cmd:"" help:"List the relays of this server"`
	Check  P2PRelaysCheckCMD  `cmd:"" help:"Check that a relay is reachable and in sync"`
}

type P2PRelaysAddCMD struct {
	P2PServerFlags `embed:""`
	Addr           string `arg:"" help:"Multiaddr of the relay, ending in /p2p/<relay peer ID>"`
	Label          string `help:"Name shown in the UI"`
	Claim          string `help:"One-time claim code the relay printed at start (use it once, it is not stored)"`
}

func (c *P2PRelaysAddCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p relays add")
}

type P2PRelaysRemoveCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" help:"Relay ID or multiaddr"`
}

func (c *P2PRelaysRemoveCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p relays remove")
}

type P2PRelaysListCMD struct {
	P2PServerFlags `embed:""`
}

func (c *P2PRelaysListCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p relays list")
}

type P2PRelaysCheckCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" optional:"" help:"Relay ID or multiaddr (default: every relay)"`
}

func (c *P2PRelaysCheckCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p relays check")
}

type P2PIDCMD struct {
	P2PServerFlags `embed:""`
}

func (c *P2PIDCMD) Run(_ *cliContext.Context) error { return errNotImplemented("local-ai p2p id") }

type P2PStatusCMD struct {
	P2PServerFlags `embed:""`
}

func (c *P2PStatusCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p status")
}

// P2PPairCMD redeems an invite on a device. It does not need a server of its own.
type P2PPairCMD struct {
	Invite string `required:"" help:"Invite link"`
	JSON   bool   `help:"Print the result as JSON"`
}

func (c *P2PPairCMD) Run(_ *cliContext.Context) error { return errNotImplemented("local-ai p2p pair") }

// P2PForwardCMD forwards a local port to a peer.
type P2PForwardCMD struct {
	Peer   string   `required:"" help:"Peer ID to forward to"`
	Listen string   `required:"" help:"Local address to listen on, for example 127.0.0.1:8080"`
	Relay  []string `help:"Relay multiaddr to try (repeatable)"`
	Addr   []string `help:"Known multiaddr of the peer (repeatable)"`
}

func (c *P2PForwardCMD) Run(_ *cliContext.Context) error {
	return errNotImplemented("local-ai p2p forward")
}
