// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
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
