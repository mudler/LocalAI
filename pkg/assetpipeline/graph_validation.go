// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"errors"
	"fmt"
	"slices"
)

func (g Graph) Node(id string) (NodeConfig, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}

	return NodeConfig{}, false
}

func outputType(n NodeConfig) ValueType {
	switch n.Kind {
	case Source, Review:
		return "text"
	case Image, Extract:
		return "image"
	case Mesh, Remesh, Rig, Skin, Retarget:
		return "mesh"
	case Motion:
		return "motion"
	}

	return ""
}

func (g Graph) accepts(n NodeConfig, typ ValueType) bool {
	switch n.Kind {
	case Source, Review, Image, Motion:
		return typ == "text"
	case Extract:
		return typ == "image"
	case Mesh:
		return (typ == "image" || typ == "text") && slices.Contains(g.Target.Inputs, string(typ))
	case Remesh, Rig, Skin, Retarget:
		return typ == "mesh"
	}

	return false
}

// Validate checks the whole graph, including inactive nodes. An incompatible
// engine leaves the configuration editable but prevents any execution.
func (g Graph) Validate() error {
	if len(g.Nodes) == 0 || len(g.Nodes) > 32 {
		return errors.New("a graph needs 1–32 nodes")
	}

	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" || len(n.ID) > 80 || seen[n.ID] {
			return errors.New("node IDs must be unique and nonempty")
		}

		seen[n.ID] = true
		if (n.Input.From == "") == (n.Input.Value == nil) {
			return fmt.Errorf("%s: choose a direct input or an earlier output", n.ID)
		}
		if n.Kind == Source && n.Input.From != "" {
			return errors.New("source must have a direct input")
		}

		for name, binding := range n.Inputs {
			if !((n.Kind == Image && name == ReferenceInput) || (n.Kind == Skin && name == "skeleton") || (n.Kind == Retarget && name == "motion")) {
				return fmt.Errorf("%s: unsupported named input %s", n.ID, name)
			}
			if (binding.From == "") == (binding.Value == nil) {
				return fmt.Errorf("%s: %s needs a direct input or an earlier output", n.ID, name)
			}
		}
		if n.Kind == Skin && len(n.Inputs) != 1 || n.Kind == Retarget && len(n.Inputs) != 1 {
			return fmt.Errorf("%s: a secondary skeleton or motion input is required", n.ID)
		}
		if n.Kind == Image && (n.Count < 1 || n.Count > 4) {
			return errors.New("image count must be between 1 and 4")
		}
		if n.Kind != Image && n.Count != 0 {
			return fmt.Errorf("%s: image count is only supported by image generation nodes", n.ID)
		}
		if n.Kind == Image && n.Count > 1 && !n.Pause {
			return errors.New("multiple images require a selection pause")
		}
	}
	if !seen[g.Primary] {
		return errors.New("primary output does not exist")
	}

	primary, _ := g.Node(g.Primary)
	if primary.Kind == Source {
		return errors.New("primary output must be a processing node, not the asset description")
	}

	earlier := map[string]bool{}
	for _, n := range g.Nodes {
		var typ ValueType
		if n.Input.Value != nil {
			typ = n.Input.Value.Type
		} else {
			p, ok := g.Node(n.Input.From)
			if !ok {
				return fmt.Errorf("%s: input node does not exist", n.ID)
			}
			if !earlier[p.ID] {
				return fmt.Errorf("%s: input must come from an earlier node", n.ID)
			}

			typ = outputType(p)
		}
		if !g.accepts(n, typ) {
			return fmt.Errorf("%s: incompatible %s input", n.ID, typ)
		}

		for name, binding := range n.Inputs {
			var typ ValueType
			if binding.Value != nil {
				typ = binding.Value.Type
			} else {
				producer, ok := g.Node(binding.From)
				if !ok || !earlier[producer.ID] {
					return fmt.Errorf("%s: %s must come from an earlier node", n.ID, name)
				}

				typ = outputType(producer)
			}
			if name == ReferenceInput && typ != "image" || name == "motion" && typ != "motion" || name == "skeleton" && typ != "mesh" && typ != "motion" {
				return fmt.Errorf("%s: incompatible %s input for %s", n.ID, typ, name)
			}
		}
		// Earlier-only bindings also rule out cycles without a separate traversal.
		earlier[n.ID] = true
	}

	return nil
}

// Order is the dependency order of only the chosen primary output's ancestors.
func (g Graph) Order() ([]string, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}

	active := map[string]bool{g.Primary: true}
	// Validation establishes topological card order. Walking it backwards
	// expands both sides of each join without running shared ancestors twice.
	for i := len(g.Nodes) - 1; i >= 0; i-- {
		n := g.Nodes[i]
		if active[n.ID] {
			for _, id := range n.Dependencies() {
				active[id] = true
			}
		}
	}

	var order []string
	for _, n := range g.Nodes {
		if active[n.ID] {
			order = append(order, n.ID)
		}
	}

	return order, nil
}

func (g Graph) Descendants(id string) map[string]bool {
	dirty := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, n := range g.Nodes {
			for _, dependency := range n.Dependencies() {
				if dirty[dependency] && !dirty[n.ID] {
					dirty[n.ID], changed = true, true
				}
			}
		}
	}

	return dirty
}

func (n NodeConfig) Dependencies() []string {
	var ids []string
	if n.Input.From != "" {
		ids = append(ids, n.Input.From)
	}

	for _, b := range n.Inputs {
		if b.From != "" {
			ids = append(ids, b.From)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
