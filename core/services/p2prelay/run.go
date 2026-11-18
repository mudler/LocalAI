package p2prelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// KeyFile is the name of the relay's private key inside Config.DataDir.
const KeyFile = "relay.key"

// DefaultPort is the port the relay listens on, for TCP and QUIC.
const DefaultPort = 4001

// RunConfig configures Run: the relay itself and how it is hosted.
type RunConfig struct {
	Config
	// Port is the port for TCP and QUIC, on IPv4 and IPv6. Zero means
	// DefaultPort. It is ignored when Listen is set.
	Port int
	// Listen replaces Port with exact listen multiaddrs.
	Listen []string
	// ResetAdmins deletes the stored admins, allowlists and passes before
	// the relay starts, so that it starts unclaimed.
	ResetAdmins bool
	// Out receives the start-up banner. Nil means os.Stdout.
	Out io.Writer
	// JSON prints the banner as one JSON line instead of text.
	JSON bool
	// OnReady is called once the relay runs, with what the banner shows.
	OnReady func(Banner)
}

// Banner is what a relay tells its operator at start: where to reach it and
// how to connect a server to it. It never holds a key.
type Banner struct {
	Version string `json:"version"`
	// Mode is "gated", "open" or "unclaimed".
	Mode    string `json:"mode"`
	PeerID  string `json:"peer_id"`
	Admins  int    `json:"admins"`
	Members int    `json:"members"`
	DataDir string `json:"data_dir"`
	// Limits is a one-line summary of the limits.
	Limits string `json:"limits"`
	// Addrs are the relay's multiaddrs with /p2p/<id>, public ones first and
	// loopback last.
	Addrs []string `json:"addrs"`
	// PublicAddr is false when none of the addresses is public.
	PublicAddr bool `json:"public_addr"`
	// ClaimCode is set while the relay is unclaimed.
	ClaimCode string `json:"claim_code,omitempty"`
	// AdminCommand is the command to run on the server.
	AdminCommand string `json:"admin_command"`
	// Warnings are lines the operator must read.
	Warnings []string `json:"warnings,omitempty"`
}

// Run starts a gated relay and blocks until ctx ends or the process gets
// SIGINT or SIGTERM. It keeps the relay's key in <DataDir>/relay.key (mode
// 0600), listens on one port for TCP and QUIC, prints the banner and stops
// the relay and its host cleanly.
func Run(ctx context.Context, cfg RunConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.DataDir == "" {
		return errors.New("p2prelay: DataDir is required")
	}
	if cfg.ResetAdmins {
		if err := os.Remove(filepath.Join(cfg.DataDir, stateFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("p2prelay: reset admins: %w", err)
		}
	}
	listen := cfg.Listen
	if len(listen) == 0 {
		listen = ListenAddrs(cfg.Port)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("p2prelay: data dir: %w", err)
	}
	key, err := p2pnet.LoadOrCreateIdentity(filepath.Join(cfg.DataDir, KeyFile))
	if err != nil {
		return err
	}
	r, err := New(cfg.Config)
	if err != nil {
		return err
	}
	opts, err := r.HostOptions()
	if err != nil {
		return err
	}
	h, err := libp2p.New(append(opts,
		libp2p.Identity(key),
		libp2p.UserAgent("localai-relay/"+Version),
		libp2p.ListenAddrStrings(listen...),
		// Servers behind the relay learn from the AutoNAT service that they
		// are private, so they start AutoRelay with no other AutoNAT peer.
		libp2p.EnableNATService(),
	)...)
	if err != nil {
		return fmt.Errorf("p2prelay: start host: %w", err)
	}
	defer func() { _ = h.Close() }()
	if err := r.Start(h); err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	b := r.Banner()
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	if cfg.JSON {
		line, err := json.Marshal(b)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, string(line))
	} else {
		_, _ = io.WriteString(out, b.Text())
	}
	if cfg.OnReady != nil {
		cfg.OnReady(b)
	}
	<-ctx.Done()
	return nil
}

// ListenAddrs returns the listen multiaddrs for one port: TCP and QUIC on
// IPv4 and IPv6. Port zero means DefaultPort.
func ListenAddrs(port int) []string {
	if port == 0 {
		port = DefaultPort
	}
	p := strconv.Itoa(port)
	return []string{
		"/ip4/0.0.0.0/tcp/" + p,
		"/ip4/0.0.0.0/udp/" + p + "/quic-v1",
		"/ip6/::/tcp/" + p,
		"/ip6/::/udp/" + p + "/quic-v1",
	}
}

// Banner describes the running relay. Call it after Start.
func (r *Relay) Banner() Banner {
	st := r.Status()
	b := Banner{
		Version: Version,
		Mode:    st.Mode,
		PeerID:  st.PeerID,
		Admins:  st.Admins,
		Members: st.Members,
		DataDir: r.cfg.DataDir,
		Limits:  r.limitsLine(),
	}
	addrs := r.Addrs()
	for _, a := range addrs {
		b.Addrs = append(b.Addrs, a.String())
		if manet.IsPublicAddr(a) {
			b.PublicAddr = true
		}
	}
	b.ClaimCode = r.ClaimCode()
	b.AdminCommand = adminCommand(addrs, b.ClaimCode)
	if !b.PublicAddr {
		b.Warnings = append(b.Warnings,
			"No public address found. Pass --public-ip <address> (needed on clouds with 1:1 NAT) or --announce <multiaddr>, or servers outside this network cannot use the relay.")
	}
	if st.Mode == "open" {
		b.Warnings = append(b.Warnings,
			"OPEN RELAY: anyone who knows this address can reserve and relay through this host. It is logged every 10 minutes.")
		if r.currentLimits().RateLimit == 0 {
			b.Warnings = append(b.Warnings,
				"OPEN RELAY WITHOUT A BANDWIDTH LIMIT: one circuit can use all the bandwidth of this host. Set --rate-limit.")
		}
	}
	return b
}

func (r *Relay) limitsLine() string {
	l := r.currentLimits()
	data, dur := "no data limit", "no time limit"
	if l.CircuitMaxBytes > 0 {
		data = formatBytes(l.CircuitMaxBytes)
	}
	if l.CircuitMaxDuration > 0 {
		dur = l.CircuitMaxDuration.String()
	}
	s := fmt.Sprintf("%s / %s per circuit, %s circuits (%s per peer), %s reservations (%s per IP)",
		data, dur, limit(l.MaxCircuits), limit(l.MaxCircuitsPerPeer), limit(l.MaxReservations), limit(l.MaxReservationsPerIP))
	if l.RateLimit > 0 {
		s += ", " + formatBytes(l.RateLimit) + "/s per circuit"
	} else {
		s += ", no bandwidth limit"
	}
	if l.TotalRateLimit > 0 {
		s += ", " + formatBytes(l.TotalRateLimit) + "/s in total"
	}
	return s
}

func limit(n int) string {
	if n == 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// adminCommand is the command for the server, with the best address: a
// public TCP address, else any public one, else the first that is not
// loopback.
func adminCommand(addrs []ma.Multiaddr, claim string) string {
	if len(addrs) == 0 {
		return ""
	}
	isTCP := func(a ma.Multiaddr) bool { _, err := a.ValueForProtocol(ma.P_TCP); return err == nil }
	pick := slices.IndexFunc(addrs, func(a ma.Multiaddr) bool { return manet.IsPublicAddr(a) && isTCP(a) })
	if pick < 0 {
		pick = slices.IndexFunc(addrs, manet.IsPublicAddr)
	}
	if pick < 0 {
		pick = slices.IndexFunc(addrs, func(a ma.Multiaddr) bool { return !manet.IsIPLoopback(a) && isTCP(a) })
	}
	if pick < 0 {
		pick = 0
	}
	cmd := "local-ai p2p relays add " + addrs[pick].String()
	if claim != "" {
		cmd += " --claim " + claim
	}
	return cmd
}

// Text formats the banner for a terminal.
func (b Banner) Text() string {
	var s strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&s, format+"\n", a...) }
	for _, warn := range b.Warnings {
		if strings.HasPrefix(warn, "OPEN RELAY") {
			w("WARNING: %s", warn)
		}
	}
	w("LocalAI relay %s  mode: %s  admins: %d  paired peers: %d  data: %s", b.Version, strings.ToUpper(b.Mode), b.Admins, b.Members, b.DataDir)
	w("limits: %s", b.Limits)
	w("PEERID %s", b.PeerID)
	for _, a := range b.Addrs {
		w("RELAY_ADDR %s", a)
	}
	for _, warn := range b.Warnings {
		if !strings.HasPrefix(warn, "OPEN RELAY") {
			w("WARNING: %s", warn)
		}
	}
	w("Firewall: allow inbound 4001/tcp and 4001/udp (or the port you set), also in the security group of your cloud.")
	if b.ClaimCode != "" {
		w("CLAIM_CODE %s", b.ClaimCode)
		w("This relay has no admin yet. The first server that sends the code becomes its admin.")
	}
	if b.AdminCommand != "" {
		w("On your LocalAI server run:")
		w("  %s", b.AdminCommand)
	}
	return s.String()
}

// ParseIP parses an IPv4 address for Config.PublicIP and says why it fails.
func ParseIP(s string) (net.IP, error) {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("%q is not an IPv4 address", s)
	}
	return ip.To4(), nil
}

// ParseAdmins parses peer IDs for Config.Admins.
func ParseAdmins(ids []string) ([]peer.ID, error) {
	var out []peer.ID
	for _, s := range ids {
		for _, one := range strings.Split(s, ",") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			id, err := peer.Decode(one)
			if err != nil {
				return nil, fmt.Errorf("admin %q is not a peer ID: %w", one, err)
			}
			out = append(out, id)
		}
	}
	return out, nil
}

// ParseAnnounce parses multiaddrs for Config.Announce.
func ParseAnnounce(addrs []string) ([]ma.Multiaddr, error) {
	var out []ma.Multiaddr
	for _, s := range addrs {
		a, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("announce address %q: %w", s, err)
		}
		out = append(out, a)
	}
	return out, nil
}
