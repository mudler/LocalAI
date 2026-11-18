package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

// P2PServerFlags say which running server the server-side commands talk to.
type P2PServerFlags struct {
	Server string `env:"LOCALAI_SERVER" default:"http://127.0.0.1:8080" help:"Base URL of the LocalAI server"`
	APIKey string `env:"LOCALAI_API_KEY" help:"API key of an admin of the server"`
	JSON   bool   `help:"Print JSON"`
}

// p2pRelayView is the part of the relay JSON of the server's REST API that
// the commands print. The commands do not import the server's types.
type p2pRelayView struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Addr      string `json:"addr"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	Health    string `json:"health"`
	Admins    int    `json:"admins"`
	Members   int    `json:"members"`
	CheckedAt string `json:"checked_at"`
	Limits    struct {
		CircuitBytes   int64 `json:"circuit_bytes"`
		CircuitSeconds int64 `json:"circuit_seconds"`
		RateBPS        int64 `json:"rate_bps"`
	} `json:"limits"`
}

// p2pCheckView is the answer of a relay check.
type p2pCheckView struct {
	Connected bool         `json:"connected"`
	Reserved  bool         `json:"reserved"`
	AdminOK   bool         `json:"admin_ok"`
	CircuitOK bool         `json:"circuit_ok"`
	InSync    bool         `json:"in_sync"`
	Detail    []string     `json:"detail"`
	Relay     p2pRelayView `json:"relay"`
}

// p2pAPI is a plain HTTP client of the server's /api/p2p routes.
type p2pAPI struct {
	base   string
	key    string
	client *http.Client
}

func (f P2PServerFlags) api() *p2pAPI {
	return &p2pAPI{base: strings.TrimRight(f.Server, "/"), key: f.APIKey, client: &http.Client{Timeout: 2 * time.Minute}}
}

// do sends a JSON request and decodes a JSON answer into out (nil to ignore
// the body). Raw is the answer's body, for --json.
func (a *p2pAPI) do(ctx context.Context, method, path string, in, out any) ([]byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("bad --server %q: %w", a.base, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.key != "" {
		req.Header.Set("Authorization", "Bearer "+a.key)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the server at %s: %w", a.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return raw, apiError(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("unexpected answer from the server: %w", err)
		}
	}
	return raw, nil
}

func apiError(status int, raw []byte) error {
	var e struct {
		Error any    `json:"error"`
		Msg   string `json:"message"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil {
		switch v := e.Error.(type) {
		case string:
			msg = v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				msg = m
			}
		}
		if msg == "" || msg == strings.TrimSpace(string(raw)) {
			if e.Msg != "" {
				msg = e.Msg
			}
		}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("the server refused the request (%d): pass an admin API key with --api-key or LOCALAI_API_KEY", status)
	case http.StatusNotFound:
		return fmt.Errorf("the server has no such P2P route or relay (404): is P2P on in \"share\" mode? %s", msg)
	}
	return fmt.Errorf("the server answered %d: %s", status, msg)
}

func (a *p2pAPI) relays(ctx context.Context) ([]p2pRelayView, []byte, error) {
	var out struct {
		Relays []p2pRelayView `json:"relays"`
	}
	raw, err := a.do(ctx, http.MethodGet, "/api/p2p/relays", nil, &out)
	return out.Relays, raw, err
}

// resolveRelay finds a relay by ID, by its address, or by the peer ID that
// ends its address.
func (a *p2pAPI) resolveRelay(ctx context.Context, ref string) (p2pRelayView, error) {
	rs, _, err := a.relays(ctx)
	if err != nil {
		return p2pRelayView{}, err
	}
	for _, r := range rs {
		if r.ID == ref || r.Addr == ref || strings.HasSuffix(r.Addr, "/p2p/"+ref) || (r.Label != "" && r.Label == ref) {
			return r, nil
		}
	}
	return p2pRelayView{}, fmt.Errorf("no relay %q on this server; run `local-ai p2p relays list`", ref)
}

func printRelays(rs []p2pRelayView) {
	if len(rs) == 0 {
		fmt.Println("No relays. Add one with `local-ai p2p relays add <multiaddr>`.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tSTATE\tMODE\tMEMBERS\tADDRESS")
	for _, r := range rs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", shortID(r.ID), r.Label, r.State, r.Mode, r.Members, r.Addr)
	}
	_ = tw.Flush()
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[len(id)-12:]
	}
	return id
}

// printRelay prints one relay for people, with the next step for its state.
func printRelay(r p2pRelayView) {
	fmt.Printf("%s  %s\n", r.ID, r.Label)
	fmt.Printf("  state:   %s\n", r.State)
	if r.Health != "" {
		fmt.Printf("  health:  %s\n", r.Health)
	}
	fmt.Printf("  mode:    %s\n", r.Mode)
	fmt.Printf("  address: %s\n", r.Addr)
	if h := relayHint(r.State); h != "" {
		fmt.Printf("  next:    %s\n", h)
	}
}

func relayHint(state string) string {
	switch state {
	case "out_of_sync":
		return "run `local-ai p2p relays sync <relay>`"
	case "unclaimed":
		return "run `local-ai p2p relays claim <relay> <code>` with the code the relay printed"
	case "open":
		return "anyone can use this relay; run it without --open and with --admin for this server"
	case "not_admin":
		return "this server is only a member; ask the relay's admin to add it or use another relay"
	case "denied":
		return "the relay does not list this server; start it with --admin <this server's peer ID> (see `local-ai p2p id`)"
	case "unreachable":
		return "check the address and that 4001/tcp and 4001/udp are open in the firewall and in the cloud security group"
	}
	return ""
}

func (f P2PServerFlags) emit(raw []byte, v any) bool {
	if !f.JSON {
		return false
	}
	if len(raw) == 0 {
		raw, _ = json.Marshal(v)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(raw)
	}
	fmt.Println(pretty.String())
	return true
}

type P2PRelaysAddCMD struct {
	P2PServerFlags `embed:""`
	Addr           string `arg:"" help:"Multiaddr of the relay, ending in /p2p/<relay peer ID>"`
	Label          string `help:"Name shown in the UI"`
	Claim          string `help:"One-time claim code the relay printed at start (use it once, it is not stored)"`
}

func (c *P2PRelaysAddCMD) Run(_ *cliContext.Context) error {
	var r p2pRelayView
	raw, err := c.api().do(context.Background(), http.MethodPost, "/api/p2p/relays",
		map[string]string{"addr": c.Addr, "label": c.Label, "claim_code": c.Claim}, &r)
	if err != nil {
		return err
	}
	if !c.emit(raw, r) {
		printRelay(r)
	}
	return nil
}

type P2PRelaysRemoveCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" help:"Relay ID or multiaddr"`
}

func (c *P2PRelaysRemoveCMD) Run(_ *cliContext.Context) error {
	api := c.api()
	r, err := api.resolveRelay(context.Background(), c.Relay)
	if err != nil {
		return err
	}
	if _, err := api.do(context.Background(), http.MethodDelete, "/api/p2p/relays/"+url.PathEscape(r.ID), nil, nil); err != nil {
		return err
	}
	if c.JSON {
		fmt.Println(`{"removed":true}`)
		return nil
	}
	fmt.Printf("Removed relay %s.\n", r.ID)
	return nil
}

type P2PRelaysListCMD struct {
	P2PServerFlags `embed:""`
}

func (c *P2PRelaysListCMD) Run(_ *cliContext.Context) error {
	rs, raw, err := c.api().relays(context.Background())
	if err != nil {
		return err
	}
	if !c.emit(raw, rs) {
		printRelays(rs)
	}
	return nil
}

type P2PRelaysCheckCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" optional:"" help:"Relay ID or multiaddr (default: every relay)"`
}

func (c *P2PRelaysCheckCMD) Run(_ *cliContext.Context) error {
	api := c.api()
	ctx := context.Background()
	var targets []p2pRelayView
	if c.Relay != "" {
		r, err := api.resolveRelay(ctx, c.Relay)
		if err != nil {
			return err
		}
		targets = []p2pRelayView{r}
	} else {
		rs, _, err := api.relays(ctx)
		if err != nil {
			return err
		}
		targets = rs
	}
	var failed []string
	var all []json.RawMessage
	for _, r := range targets {
		var chk p2pCheckView
		raw, err := api.do(ctx, http.MethodPost, "/api/p2p/relays/"+url.PathEscape(r.ID)+"/check", nil, &chk)
		if err != nil {
			return err
		}
		all = append(all, raw)
		if !c.JSON {
			printRelay(chk.Relay)
			for _, d := range chk.Detail {
				fmt.Printf("  - %s\n", d)
			}
		}
		if chk.Relay.State != "in_sync" && chk.Relay.State != "open" {
			failed = append(failed, r.ID)
		}
	}
	if c.JSON {
		b, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(b))
	}
	if len(failed) > 0 {
		return ExitCodeError{Code: 1}
	}
	return nil
}

// P2PRelaysSyncCMD pushes the paired devices to a relay.
type P2PRelaysSyncCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" help:"Relay ID or multiaddr"`
}

func (c *P2PRelaysSyncCMD) Run(_ *cliContext.Context) error {
	api := c.api()
	r, err := api.resolveRelay(context.Background(), c.Relay)
	if err != nil {
		return err
	}
	var out p2pRelayView
	raw, err := api.do(context.Background(), http.MethodPost, "/api/p2p/relays/"+url.PathEscape(r.ID)+"/sync", nil, &out)
	if err != nil {
		return err
	}
	if !c.emit(raw, out) {
		printRelay(out)
	}
	return nil
}

// P2PRelaysClaimCMD claims an unclaimed relay.
type P2PRelaysClaimCMD struct {
	P2PServerFlags `embed:""`
	Relay          string `arg:"" help:"Relay ID or multiaddr"`
	Code           string `arg:"" help:"Claim code the relay printed at start"`
}

func (c *P2PRelaysClaimCMD) Run(_ *cliContext.Context) error {
	api := c.api()
	r, err := api.resolveRelay(context.Background(), c.Relay)
	if err != nil {
		return err
	}
	var out p2pRelayView
	raw, err := api.do(context.Background(), http.MethodPost, "/api/p2p/relays/"+url.PathEscape(r.ID)+"/claim",
		map[string]string{"code": c.Code}, &out)
	if err != nil {
		return err
	}
	if !c.emit(raw, out) {
		printRelay(out)
	}
	return nil
}

// P2PStatusCMD shows the state of the server's P2P layer.
type P2PStatusCMD struct {
	P2PServerFlags `embed:""`
}

func (c *P2PStatusCMD) Run(_ *cliContext.Context) error {
	var st struct {
		Enabled      bool     `json:"enabled"`
		Mode         string   `json:"mode"`
		PeerID       string   `json:"peer_id"`
		Addrs        []string `json:"addrs"`
		Reachability string   `json:"reachability"`
		Reservations []string `json:"reservations"`
	}
	raw, err := c.api().do(context.Background(), http.MethodGet, "/api/p2p/status", nil, &st)
	if err != nil {
		return err
	}
	if c.emit(raw, st) {
		return nil
	}
	mode := st.Mode
	if mode == "" {
		mode = map[bool]string{true: "on", false: "off"}[st.Enabled]
	}
	fmt.Printf("mode:         %s\n", mode)
	if st.PeerID == "" {
		fmt.Println("P2P is off on this server.")
		return nil
	}
	fmt.Printf("peer ID:      %s\n", st.PeerID)
	fmt.Printf("reachability: %s\n", st.Reachability)
	for _, a := range st.Addrs {
		fmt.Printf("address:      %s\n", a)
	}
	for _, r := range st.Reservations {
		fmt.Printf("reserved on:  %s\n", r)
	}
	return nil
}
