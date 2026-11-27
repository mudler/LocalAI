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
	Node string `json:"node"`
	// Enabled is false when the tier is off on the node; Error says why a
	// node gave no usage (offline, no answer).
	Enabled bool `json:"enabled"`
	// OffReason says why the tier is off on the node: no_path,
	// switched_off, network_disk or not_writable.
	OffReason  string       `json:"off_reason,omitempty"`
	Error      string       `json:"error,omitempty"`
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
	// Total sums the nodes that answered.
	Total Total `json:"total"`
}

// Total is the disk tier of every node that answered.
type Total struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	// CapBytes sums the effective caps of the nodes.
	CapBytes int64 `json:"cap_bytes"`
	// Nodes counts the nodes that answered, Unreachable the ones that did
	// not.
	Nodes       int `json:"nodes"`
	Unreachable int `json:"unreachable"`
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
			Node: r.Node, Enabled: r.Enabled, OffReason: r.OffReason, Error: r.Error, Bytes: r.Bytes, Files: r.Files, Namespaces: len(r.Namespaces),
			Status: r.Status, Models: []ModelUsage{},
		}
		if r.Error != "" && r.Files == 0 && r.Status.State == "" {
			resp.Total.Unreachable++
		} else {
			resp.Total.Nodes++
			resp.Total.Bytes += r.Bytes
			resp.Total.Files += r.Files
			resp.Total.CapBytes += r.Status.EffectiveCapBytes
		}
		for id, u := range r.Models {
			ns.Models = append(ns.Models, ModelUsage{Model: names[id], Model16: id, Bytes: u.Bytes, Files: u.Files})
		}
		sort.Slice(ns.Models, func(i, j int) bool { return ns.Models[i].Bytes > ns.Models[j].Bytes })
		resp.Nodes = append(resp.Nodes, ns)
	}
	return resp, nil
}
