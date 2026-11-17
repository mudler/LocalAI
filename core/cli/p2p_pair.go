package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
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

type redeemResponse struct {
	DeviceID string `json:"device_id"`
	Kind     string `json:"kind"`
	Phrase   string `json:"phrase"`
	Server   struct {
		PeerID  string `json:"peer_id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"server"`
	ClientKey *struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	} `json:"client_key,omitempty"`
}

// pairRecord is what a saved pairing holds.
type pairRecord struct {
	ServerPeer string   `json:"server_peer"`
	ServerName string   `json:"server_name,omitempty"`
	Kind       string   `json:"kind"`
	DeviceID   string   `json:"device_id"`
	Relays     []string `json:"relays,omitempty"`
	Addrs      []string `json:"addrs,omitempty"`
	ClientKey  *struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	} `json:"client_key,omitempty"`
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

func (c *P2PPairCMD) Run(_ *cliContext.Context) error {
	link, err := p2ppair.ParseLink(c.Invite)
	if err != nil {
		return err
	}
	if link.Expired(time.Now()) {
		return errors.New("the invite has expired: ask for a new one")
	}
	if len(link.Open) > 0 && !c.AllowOpenRelay {
		return errors.New("the invite names an open relay, which anyone can use; pass --allow-open-relay to connect through it anyway")
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

	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: identity, ListenAddrs: []string{}})
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	// The relay pass lets this still-unknown device through the relay. The
	// link does not say which relay holds it, so try the gated ones.
	if link.Pass != "" {
		for i, r := range link.Relays {
			if slices.Contains(link.Open, i) {
				continue
			}
			cctx, ccancel := context.WithTimeout(ctx, 15*time.Second)
			err := h.Libp2p().Connect(cctx, r)
			ccancel()
			if err != nil {
				continue
			}
			if _, err := relayproto.UsePass(ctx, h.Libp2p(), r.ID, link.Pass); err == nil {
				break
			}
		}
	}

	opts := []p2pnet.DialOption{p2pnet.WithRelays(link.Relays...), p2pnet.WithAddrs(link.Addrs...)}
	name := c.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	body, err := json.Marshal(map[string]any{"v": 1, "code": link.Code, "name": name, "platform": runtime.GOOS})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+link.Peer.String()+"/api/p2p/redeem", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTPClient(link.Peer, opts...).Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the server named in the invite: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone:
		return errors.New("the invite is not valid any more: it was used, cancelled or expired")
	case http.StatusTooManyRequests:
		return errors.New("too many wrong codes from this device or relay: wait a while and try again")
	default:
		return apiError(resp.StatusCode, raw)
	}
	var out redeemResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("unexpected answer from the server: %w", err)
	}
	if out.Server.PeerID != "" && out.Server.PeerID != link.Peer.String() {
		return fmt.Errorf("the answering server is %s, not the %s of the invite: do not trust this pairing", out.Server.PeerID, link.Peer)
	}

	var saved string
	if persist {
		if saved, err = c.save(identity, link, out); err != nil {
			return err
		}
	}
	return c.report(out, link, saved)
}

func (c *P2PPairCMD) save(identity string, link *p2ppair.Link, out redeemResponse) (string, error) {
	dir := filepath.Join(filepath.Dir(identity), "pairs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	rec := pairRecord{ServerPeer: link.Peer.String(), ServerName: out.Server.Name, Kind: out.Kind, DeviceID: out.DeviceID, ClientKey: out.ClientKey}
	for _, r := range link.Relays {
		for _, a := range r.Addrs {
			rec.Relays = append(rec.Relays, a.String()+"/p2p/"+r.ID.String())
		}
	}
	for _, a := range link.Addrs {
		rec.Addrs = append(rec.Addrs, a.String())
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

func (c *P2PPairCMD) report(out redeemResponse, link *p2ppair.Link, saved string) error {
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
