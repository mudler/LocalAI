package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/pkg/logsetup"
)

// P2PPairCMD redeems an invite on a device. It does not need a server of its
// own. Without --identity or --data-path nothing is written to disk and the
// pairing is forgotten when the command ends: the server admits the peer ID
// of the identity, so a device that wants to use the pairing later must keep
// its identity file.
type P2PPairCMD struct {
	Invite         string `required:"" help:"Invite link (localai://pair?...)"`
	Name           string `help:"Name of this device, shown on the server (default: the host name)"`
	Identity       string `env:"LOCALAI_P2P_IDENTITY" type:"path" help:"Identity file to create or reuse. Needed to keep the pairing"`
	DataPath       string `type:"path" help:"Same as --identity <path>/p2p/identity.key"`
	AllowOpenRelay bool   `help:"Connect through an open relay that the invite names. Anyone can use an open relay; the traffic is encrypted end to end, but the relay sees who talks to whom"`
	JSON           bool   `help:"Print the result as JSON, with the client key of a remote invite"`
}

// pairRecord is what a saved pairing holds.
type pairRecord struct {
	ServerPeer string                `json:"server_peer"`
	ServerName string                `json:"server_name,omitempty"`
	Kind       string                `json:"kind"`
	DeviceID   string                `json:"device_id"`
	Relays     []string              `json:"relays,omitempty"`
	Addrs      []string              `json:"addrs,omitempty"`
	ClientKey  *pairclient.ClientKey `json:"client_key,omitempty"`
}

func (c *P2PPairCMD) identityPath() string {
	switch {
	case c.Identity != "":
		return c.Identity
	case c.DataPath != "":
		return filepath.Join(c.DataPath, "p2p", "identity.key")
	}
	return ""
}

func (c *P2PPairCMD) Run(cctx *cliContext.Context) error {
	if c.JSON {
		// Standard output carries only the JSON result.
		logsetup.SetLogger(logsetup.NewLogSettings(cctx.Debug, cctx.LogLevel, cctx.LogFormat, cctx.LogDedupLogs), true)
	}
	link, err := p2ppair.ParseLink(c.Invite)
	if err != nil {
		return err
	}
	// Check the link before anything is written, so a link that cannot be
	// used leaves no identity behind.
	if link.Expired(time.Now()) {
		return pairclient.ErrExpired
	}
	if len(link.Open) > 0 && !c.AllowOpenRelay {
		return fmt.Errorf("%w; pass --allow-open-relay to connect through it anyway", pairclient.ErrOpenRelay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	identity := c.identityPath()
	persist := identity != ""
	if !persist {
		dir, err := os.MkdirTemp("", "localai-pair-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		identity = filepath.Join(dir, "identity.key")
		_, _ = fmt.Fprintln(os.Stderr, "Note: no --identity or --data-path, so this pairing is not saved and this device is forgotten when the command ends.")
	}

	// The host listens on ports that the system picks, so that a hole punch
	// can reach it; its empty allowlist refuses every inbound connection.
	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: identity})
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	name := c.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	out, err := pairclient.Redeem(ctx, h, link, pairclient.Request{Name: name, Platform: runtime.GOOS, AllowOpenRelay: c.AllowOpenRelay})
	if err != nil {
		return err
	}

	var saved string
	if persist {
		if saved, err = c.save(identity, link, out); err != nil {
			return err
		}
	}
	return c.report(out, link, saved)
}

func (c *P2PPairCMD) save(identity string, link *p2ppair.Link, out *pairclient.Result) (string, error) {
	dir := filepath.Join(filepath.Dir(identity), "pairs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	rec := pairRecord{
		ServerPeer: link.Peer.String(), ServerName: out.Server.Name, Kind: out.Kind, DeviceID: out.DeviceID, ClientKey: out.ClientKey,
		Relays: link.RelayAddrStrings(), Addrs: link.AddrStrings(),
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, link.Peer.String()+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (c *P2PPairCMD) report(out *pairclient.Result, link *p2ppair.Link, saved string) error {
	if c.JSON {
		view := map[string]any{
			"device_id": out.DeviceID, "kind": out.Kind, "phrase": out.Phrase,
			"server": map[string]string{"peer_id": link.Peer.String(), "name": out.Server.Name, "version": out.Server.Version},
		}
		if out.ClientKey != nil {
			view["client_key"] = out.ClientKey
		}
		if saved != "" {
			view["saved"] = saved
		}
		return json.NewEncoder(os.Stdout).Encode(view)
	}
	name := out.Server.Name
	if name == "" {
		name = link.Peer.String()
	}
	fmt.Printf("Paired with %s as a %s.\n", name, out.Kind)
	fmt.Printf("Confirmation phrase: %s\n", out.Phrase)
	fmt.Println("Check that the server shows the same four words. If they differ, the link was swapped: remove this device on the server.")
	if out.ClientKey != nil {
		fmt.Println("The server issued a client key. Run again with --json to print it, or use --identity or --data-path to save it.")
	}
	if saved != "" {
		fmt.Printf("Saved to %s\n", saved)
	}
	return nil
}
