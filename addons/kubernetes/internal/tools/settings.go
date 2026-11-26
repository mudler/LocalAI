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
	res, err := kubeapi.Check(c.ctx, c.c, a.Namespace)
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
	all, err := r.Policy.All()
	broken := err != nil
	if broken {
		all = map[string]policy.Policy{}
	}
	return finish(map[string]any{
		"clusters":   all,
		"defaults":   policy.Defaults(),
		"writeTools": policy.WriteTools,
		"admin":      r.isAdmin(ctx, env),
		"broken":     broken,
	}, nil)
}

type setPolicyArgs struct {
	Key              string    `json:"key"`
	Namespaces       *[]string `json:"namespaces"`
	WriteTools       *[]string `json:"writeTools"`
	ReadSecretValues *bool     `json:"readSecretValues"`
}

// setPolicy replaces the fields of one cluster's policy that the page names.
// Only an administrator may call it, and nothing is saved when a field is
// refused.
func (r *Runtime) setPolicy(ctx context.Context, env contract.Envelope, args map[string]json.RawMessage) (contract.Result, error) {
	if !r.isAdmin(ctx, env) {
		return contract.Result{}, bad("only an administrator can change the policy")
	}
	var a setPolicyArgs
	if err := decodeArgs(args, &a); err != nil {
		return contract.Result{}, err
	}
	conn, err := kubeapi.ParseKey(a.Key)
	if err != nil {
		return contract.Result{}, bad("%s", err.Error())
	}
	p, err := r.Policy.Get(conn.Key())
	if err != nil {
		// A damaged file is replaced, starting from the defaults.
		p = policy.Defaults()
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
	saved, err := r.Policy.Set(conn.Key(), p)
	if err != nil {
		return contract.Result{}, bad("%s", err.Error())
	}
	return finish(map[string]any{"saved": true, "key": conn.Key(), "policy": saved}, nil)
}
