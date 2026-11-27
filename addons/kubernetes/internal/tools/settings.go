// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type checkArgs struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
}

// check reports the version of the server and the RBAC rules of the
// credential in one namespace. The page runs it after a save.
func (c *call) check(raw map[string]json.RawMessage) (any, error) {
	var a checkArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	ns := a.Namespace
	if ns == "" {
		// The page names no namespace: check default, or the first
		// namespace the policy allows.
		ns = "default"
		if !c.pol.NamespaceAllowed(ns) && len(c.pol.Namespaces) > 0 {
			ns = c.pol.Namespaces[0]
		}
	}
	if err := c.allow(ns); err != nil {
		return nil, err
	}
	res, err := kubeapi.Check(c.ctx, c.c, ns)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cluster": c.key, "server": c.conn.Server, "context": c.conn.Context, "check": res}, nil
}

func (r *Runtime) isAdmin(ctx context.Context, env contract.Envelope) bool {
	return r.Admin != nil && r.Admin.IsAdmin(ctx, env.Request.OperationID)
}

// getPolicy is the page's read of the policies. Anyone who may open the page
// sees them; only an administrator sees them as editable.
func (r *Runtime) getPolicy(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	v := r.Policy.View()
	return finish(map[string]any{
		"clusters":       v.Clusters,
		"default":        v.Default,
		"defaultSet":     v.DefaultSet,
		"builtinDefault": policy.Defaults(),
		"writeTools":     policy.WriteTools,
		"admin":          r.isAdmin(ctx, env),
		"broken":         v.Broken,
		"legacy":         v.Legacy,
		"invalid":        v.Invalid,
	}, nil)
}

type setPolicyArgs struct {
	Key              string    `json:"key"`
	Default          bool      `json:"default"`
	Namespaces       *[]string `json:"namespaces"`
	WriteTools       *[]string `json:"writeTools"`
	ReadSecretValues *bool     `json:"readSecretValues"`
}

// setPolicy replaces the fields of one policy that the page names: the
// policy of a cluster (key, its server URL or a connection key) or the
// default policy (default true). Only an administrator may call it, and
// nothing is saved when a field is refused. A field the page leaves out keeps
// the value the cluster has now; while the file is damaged that is the
// Damaged policy, which allows no namespace and no write, so a save that
// leaves out the namespaces is refused until it names them.
func (r *Runtime) setPolicy(ctx context.Context, env contract.Envelope, args map[string]json.RawMessage) (contract.Result, error) {
	if !r.isAdmin(ctx, env) {
		return contract.Result{}, bad("only an administrator can change the policy")
	}
	var a setPolicyArgs
	if err := decodeArgs(args, &a); err != nil {
		return contract.Result{}, err
	}
	var p policy.Policy
	server := ""
	switch {
	case a.Default && a.Key != "":
		return contract.Result{}, bad("name a cluster or the default policy, not both")
	case a.Default:
		p = r.Policy.View().Default
	default:
		s, err := policy.ServerKey(a.Key)
		if err != nil {
			return contract.Result{}, bad("the key is the server URL of a cluster: %s", err.Error())
		}
		server = s
		if p, err = r.Policy.Get(server); err != nil {
			return contract.Result{}, bad("%s", err.Error())
		}
	}
	if a.Namespaces != nil {
		p.Namespaces = *a.Namespaces
	}
	if a.WriteTools != nil {
		p.WriteTools = *a.WriteTools
	}
	if a.ReadSecretValues != nil {
		p.ReadSecretValues = *a.ReadSecretValues
	}
	var saved policy.Policy
	var err error
	if a.Default {
		saved, err = r.Policy.SetDefault(p)
	} else {
		saved, err = r.Policy.Set(server, p)
	}
	if err != nil {
		return contract.Result{}, bad("%s", err.Error())
	}
	return finish(map[string]any{"saved": true, "key": server, "default": a.Default, "policy": saved}, nil)
}
