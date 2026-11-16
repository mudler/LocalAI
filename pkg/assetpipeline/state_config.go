// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

func stageLabel(g Graph, id string) string {
	n, ok := g.Node(id)
	if !ok {
		return id
	}
	if n.Kind == Source && n.ID == "motion-description" {
		return "Motion description"
	}
	if n.Kind == Review && n.Params["purpose"] == "motion" {
		return "Motion prompt review"
	}

	labels := map[Kind]string{
		Source:   "Asset description",
		Review:   "Prompt review",
		Image:    "Image generation",
		Extract:  "Object extraction",
		Mesh:     "3D generation",
		Remesh:   "Remeshing",
		Rig:      "Generate rig and weights",
		Skin:     "Skin to skeleton",
		Motion:   "Motion generation",
		Retarget: "Apply motion",
	}
	if label, ok := labels[n.Kind]; ok {
		return label
	}

	return id
}

func configValue(v any) string {
	// The complete values live in the immutable checkpoint. Keep status messages
	// readable even when descriptions or segmentation coordinates are large.
	value := strings.Join(strings.Fields(fmt.Sprint(v)), " ")
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120]) + "…"
	}
	if value == "" {
		return "(empty)"
	}

	return value
}

func inputLabel(g Graph, input Binding) string {
	if input.From != "" {
		return "output from " + stageLabel(g, input.From)
	}
	if input.Value == nil {
		return "(none)"
	}
	if input.Value.Type == schema.TextValue {
		return "direct text: " + configValue(input.Value.Text)
	}

	return "direct " + string(input.Value.Type) + ": " + configValue(input.Value.Artifact)
}

func configurationMessage(old, graph Graph) string {
	var changes []string
	add := func(label string, before, after any) {
		if !reflect.DeepEqual(before, after) {
			changes = append(changes, label+": "+configValue(before)+" → "+configValue(after))
		}
	}
	if old.Primary != graph.Primary {
		changes = append(changes, "Primary output: "+stageLabel(old, old.Primary)+" → "+stageLabel(graph, graph.Primary))
	}

	for _, node := range graph.Nodes {
		label := stageLabel(graph, node.ID)
		previous, exists := old.Node(node.ID)
		if !exists {
			changes = append(changes, label+": stage added")
		}
		add(label+" type", previous.Kind, node.Kind)
		add(label+" model", previous.Model, node.Model)

		if !reflect.DeepEqual(previous.Input, node.Input) {
			changes = append(changes, label+" input: "+inputLabel(old, previous.Input)+" → "+inputLabel(graph, node.Input))
		}

		inputNames := make([]string, 0, len(previous.Inputs)+len(node.Inputs))
		for name := range previous.Inputs {
			inputNames = append(inputNames, name)
		}

		for name := range node.Inputs {
			inputNames = append(inputNames, name)
		}
		slices.Sort(inputNames)
		for _, name := range slices.Compact(inputNames) {
			if !reflect.DeepEqual(previous.Inputs[name], node.Inputs[name]) {
				changes = append(changes, label+" "+name+" input: "+inputLabel(old, previous.Inputs[name])+" → "+inputLabel(graph, node.Inputs[name]))
			}
		}
		add(label+" pause for selection", previous.Pause, node.Pause)
		add(label+" image count", previous.Count, node.Count)
		add(label+" seed", previous.Seed, node.Seed)

		keys := make([]string, 0, len(previous.Params)+len(node.Params))
		for key := range previous.Params {
			keys = append(keys, key)
		}

		for key := range node.Params {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range slices.Compact(keys) {
			before, had := previous.Params[key]
			after, has := node.Params[key]
			if had != has && before == after {
				changes = append(changes, label+" "+strings.ReplaceAll(key, "_", " ")+": empty setting "+map[bool]string{true: "added", false: "removed"}[has])
			} else {
				add(label+" "+strings.ReplaceAll(key, "_", " "), before, after)
			}
		}
	}

	for _, node := range old.Nodes {
		if _, exists := graph.Node(node.ID); !exists {
			changes = append(changes, stageLabel(old, node.ID)+": stage removed")
		}
	}
	add("3D engine profile", old.Target.Model, graph.Target.Model)
	add("3D engine accepted inputs", old.Target.Inputs, graph.Target.Inputs)
	add("3D engine prompting advice", old.Target.Advice, graph.Target.Advice)
	add("3D engine profile version", old.Target.Version, graph.Target.Version)

	if len(changes) == 0 {
		return "Configuration saved: stage order or configuration structure updated."
	}

	return "Configuration saved:\n" + strings.Join(changes, "\n")
}
