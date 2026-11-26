// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// call is one tool call: the cluster the host allowed and its client.
type call struct {
	r        *Runtime
	ctx      context.Context
	key      string
	conn     kubeapi.Conn
	c        *kubeapi.Client
	pol      policy.Policy
	warnings []string
	// deadline is when an apply must end, on the clock of the runtime.
	deadline time.Time
}

// newCall checks the cluster of the envelope: the host allowed one cluster
// for this call, it must parse, and a cluster argument must name that one.
// The credential never goes into an error.
func (r *Runtime) newCall(ctx context.Context, env contract.Envelope, clusterArg string) (*call, error) {
	if env.Request.Credential == "" {
		return nil, bad("the connection has no kubeconfig: ask the person to add one in the connection settings")
	}
	key := env.Request.ResourceKey
	if key == "" {
		return nil, bad("the connection lists no cluster")
	}
	if a := strings.TrimSpace(clusterArg); a != "" && !strings.EqualFold(a, key) {
		return nil, bad("cluster is not the one the host allowed")
	}
	conn, err := kubeapi.ParseKey(key)
	if err != nil {
		return nil, bad("%s", err.Error())
	}
	cfg, err := kubeapi.ParseKubeconfig(env.Request.Credential, conn)
	if err != nil {
		return nil, bad("%s. Ask the person to fix the connection", err.Error())
	}
	pol, err := r.Policy.Get(conn.Server)
	if err != nil {
		return nil, bad("%s", err.Error())
	}
	c, err := r.Factory(cfg)
	if err != nil {
		return nil, bad("the client of the cluster could not be built")
	}
	return &call{r: r, ctx: ctx, key: conn.Key(), conn: conn, c: c, pol: pol}, nil
}

// close closes the idle connections of the client of the call.
func (c *call) close() { c.c.Close() }
