// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/addons/ssh/internal/credential"
	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// info checks the connection: it logs in with the pinned host key and asks the
// machine for its uname and the name of the user. A machine that has no such
// command (not Unix) still answers "connected"; the fields are then empty.
func (c *call) info() (map[string]any, error) {
	unlock, err := c.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ask := func(cmd string) string {
		res, err := conn.Run(c.ctx, cmd, infoTimeout, nil, c.cred.Scrub)
		if err != nil || res.ExitCode != 0 || res.TimedOut {
			return ""
		}
		return short(strings.TrimSpace(res.Stdout), 300)
	}
	method := string(credential.KindKey)
	if c.cred.Kind == credential.KindPassword {
		method = string(credential.KindPassword)
	}
	out := map[string]any{
		"connected":          true,
		"host":               c.target.Key(),
		"login":              method,
		"hostKeyFingerprint": sshclient.FingerprintOfPin(c.target.Pin),
		"uname":              ask("uname -a"),
		"user":               ask("id -un"),
	}
	return out, nil
}

// scan reads the host key of an address for the page. It is the one call that
// has no credential: the handshake stops at the host key, so nothing secret is
// sent. The page shows the key to the person, who decides whether to trust it.
func (r *Runtime) scan(ctx context.Context, raw map[string]json.RawMessage) (contract.Result, error) {
	var a struct {
		Address string `json:"address"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return contract.Result{}, err
	}
	t, err := sshclient.ParseTarget(a.Address)
	if err != nil {
		return contract.Result{}, bad("%s", err)
	}
	if t.Pin != "" {
		return contract.Result{}, bad("give the address without a host key pin")
	}
	k, err := sshclient.ScanHostKey(ctx, t)
	if err != nil {
		return contract.Result{}, translate(err)
	}
	return finish(map[string]any{"address": t.Key(), "keyType": k.Type, "fingerprint": k.Fingerprint, "pin": k.Pin})
}
