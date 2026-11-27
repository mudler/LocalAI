package promptcachesvc

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/promptcache"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
)

// ModelUsage is the disk prompt cache of one model on one node.
type ModelUsage struct {
	// Model is the name of the model configuration, empty when no installed
	// model has these files (a deleted model, or a changed model file).
	Model   string `json:"model,omitempty"`
	Model16 string `json:"model16"`
	Bytes   int64  `json:"bytes"`
	Files   int    `json:"files"`
}

// NodeStatus is the disk prompt cache of one node.
type NodeStatus struct {
	Node       string       `json:"node"`
	Bytes      int64        `json:"bytes"`
	Files      int          `json:"files"`
	Namespaces int          `json:"namespaces"`
	Status     pc.Status    `json:"status"`
	Models     []ModelUsage `json:"models"`
}

// StatusReport is the disk prompt cache of every node a Nodes reaches.
type StatusReport struct {
	// Enabled is the global switch with a place to keep the files.
	Enabled bool `json:"enabled"`
	// Shared is true when every user shares one namespace.
	Shared bool         `json:"shared"`
	Nodes  []NodeStatus `json:"nodes"`
}

// ModelNames maps the model16 id of each model configuration to its name.
func ModelNames(cl *config.ModelConfigLoader) map[string]string {
	names := map[string]string{}
	if cl == nil {
		return names
	}
	for _, m := range cl.GetAllModelsConfigs() {
		if m.Model != "" {
			names[promptcache.ModelID(m.Model)] = m.Name
		}
	}
	return names
}

// Report builds the status of the nodes, with the model names.
func Report(ctx context.Context, nodes Nodes, enabled, shared bool, names map[string]string) (StatusReport, error) {
	resp := StatusReport{Enabled: enabled, Shared: shared, Nodes: []NodeStatus{}}
	reports, err := nodes.Usage(ctx)
	if err != nil {
		return resp, err
	}
	for _, r := range reports {
		ns := NodeStatus{
			Node: r.Node, Bytes: r.Bytes, Files: r.Files, Namespaces: len(r.Namespaces),
			Status: r.Status, Models: []ModelUsage{},
		}
		for id, u := range r.Models {
			ns.Models = append(ns.Models, ModelUsage{Model: names[id], Model16: id, Bytes: u.Bytes, Files: u.Files})
		}
		sort.Slice(ns.Models, func(i, j int) bool { return ns.Models[i].Bytes > ns.Models[j].Bytes })
		resp.Nodes = append(resp.Nodes, ns)
	}
	return resp, nil
}
