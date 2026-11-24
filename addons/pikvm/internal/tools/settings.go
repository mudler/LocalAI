// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/addons/pikvm/internal/settings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func (r *Runtime) isAdmin(ctx context.Context, env contract.Envelope) bool {
	return r.Admin != nil && r.Admin.IsAdmin(ctx, env.Request.OperationID)
}

// getSettings is the page's read of the per-device switches. Anyone who may
// open the page sees them; only an administrator sees them as editable.
func (r *Runtime) getSettings(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	all, err := r.Settings.All()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		d := all[k]
		rows = append(rows, map[string]any{"device": k, "allowHardPower": d.AllowHardPower, "allowTyping": d.AllowTyping, "maxTypeChars": d.MaxTypeChars})
	}
	return finish(map[string]any{
		"devices": rows, "admin": r.isAdmin(ctx, env), "broken": err != nil,
		"defaults": map[string]any{"allowHardPower": false, "allowTyping": false, "maxTypeChars": settings.DefaultTypeChars},
		"limits":   map[string]any{"maxTypeChars": settings.MaxTypeChars},
		"keys":     AllowedKeys(),
	})
}

type setSettingsArgs struct {
	Device         string `json:"device"`
	AllowHardPower *bool  `json:"allowHardPower"`
	AllowTyping    *bool  `json:"allowTyping"`
	MaxTypeChars   *int   `json:"maxTypeChars"`
}

// setSettings changes the fields the page names for one device. Only an
// administrator may call it.
func (r *Runtime) setSettings(ctx context.Context, env contract.Envelope, args map[string]json.RawMessage) (contract.Result, error) {
	if !r.isAdmin(ctx, env) {
		return contract.Result{}, bad("only an administrator can change these settings")
	}
	var a setSettingsArgs
	if err := decodeArgs(args, &a); err != nil {
		return contract.Result{}, err
	}
	dev, err := kvmd.ParseDevice(a.Device)
	if err != nil {
		return contract.Result{}, bad("%s", kvmd.Describe(err))
	}
	cur, _ := r.Settings.For(dev.Key())
	if a.AllowHardPower != nil {
		cur.AllowHardPower = *a.AllowHardPower
	}
	if a.AllowTyping != nil {
		cur.AllowTyping = *a.AllowTyping
	}
	if a.MaxTypeChars != nil {
		if *a.MaxTypeChars < 1 || *a.MaxTypeChars > settings.MaxTypeChars {
			return contract.Result{}, bad("maxTypeChars is between 1 and %d", settings.MaxTypeChars)
		}
		cur.MaxTypeChars = *a.MaxTypeChars
	}
	saved, err := r.Settings.Set(dev.Key(), cur)
	if err != nil {
		return contract.Result{}, bad("%s", err.Error())
	}
	return finish(map[string]any{"saved": true, "device": dev.Key(), "allowHardPower": saved.AllowHardPower, "allowTyping": saved.AllowTyping, "maxTypeChars": saved.MaxTypeChars})
}
