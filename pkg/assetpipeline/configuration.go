// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

type ConfigurationPath string

const (
	KindPath       ConfigurationPath = "/kind"
	ModelPath      ConfigurationPath = "/model"
	InputPath      ConfigurationPath = "/input"
	InputsPath     ConfigurationPath = "/inputs"
	PausePath      ConfigurationPath = "/pause"
	CountPath      ConfigurationPath = "/count"
	SeedPath       ConfigurationPath = "/seed"
	ParamsPath     ConfigurationPath = "/params"
	PrimaryPath    ConfigurationPath = "/primary"
	TargetPath     ConfigurationPath = "/target"
	OrderPath      ConfigurationPath = "/order"
	RemoveNodePath ConfigurationPath = "/"
)

func configurationType(path ConfigurationPath) schema.DataType {
	switch path {
	case KindPath, ModelPath, PrimaryPath:
		return schema.String
	case PausePath:
		return schema.Boolean
	case CountPath, SeedPath:
		return schema.Integer
	case OrderPath:
		return schema.Array
	case RemoveNodePath:
		return schema.Null
	case InputPath, InputsPath, ParamsPath, TargetPath:
		return schema.Object
	default:
		return ""
	}
}

func validateConfiguration(e *Event) error {
	graphField := e.Path == PrimaryPath || e.Path == TargetPath || e.Path == OrderPath
	if (e.Node == "") != graphField {
		return fmt.Errorf("invalid configuration path %q", e.Path)
	}
	if e.ValueType == schema.Null && e.Value == nil && (e.Path == InputsPath || e.Path == ParamsPath || e.Path == RemoveNodePath) {
		return nil
	}
	if e.ValueType != configurationType(e.Path) {
		return fmt.Errorf("invalid type for configuration path %q", e.Path)
	}
	valid := false
	switch e.Path {
	case KindPath:
		_, valid = e.Value.(Kind)
	case ModelPath, PrimaryPath:
		_, valid = e.Value.(string)
	case InputPath:
		_, valid = e.Value.(Binding)
	case InputsPath:
		_, valid = e.Value.(map[string]Binding)
	case ParamsPath:
		_, valid = e.Value.(map[string]string)
	case PausePath:
		_, valid = e.Value.(bool)
	case CountPath:
		_, valid = e.Value.(int)
	case SeedPath:
		_, valid = e.Value.(int64)
	case TargetPath:
		_, valid = e.Value.(Profile)
	case OrderPath:
		_, valid = e.Value.([]string)
	}
	if !valid {
		return fmt.Errorf("invalid value for configuration path %q", e.Path)
	}
	return nil
}

func applyConfigurationEvent(s *Snapshot, e *Event) error {
	if err := validateConfiguration(e); err != nil {
		return err
	}
	// Copy-on-write: another replay projection may share the current graph.
	g := copyGraph(s.Graph)
	if e.Node == "" {
		switch e.Path {
		case PrimaryPath:
			g.Primary = e.Value.(string)
		case TargetPath:
			g.Target = copyProfile(e.Value.(Profile))
		case OrderPath:
			ids := e.Value.([]string)
			if len(ids) != len(g.Nodes) {
				return errors.New("node order must contain every node")
			}

			ordered := make([]NodeConfig, 0, len(ids))
			seen := map[string]bool{}
			for _, id := range ids {
				n, ok := g.Node(id)
				if !ok || seen[id] {
					return errors.New("invalid node order")
				}

				seen[id] = true
				ordered = append(ordered, n)
			}

			g.Nodes = ordered
		default:
			return fmt.Errorf("unknown graph configuration path %q", e.Path)
		}
	} else {
		index := slices.IndexFunc(g.Nodes, func(n NodeConfig) bool { return n.ID == e.Node })
		if e.Path == KindPath && index < 0 {
			if e.Node == "" || len(e.Node) > 80 || len(g.Nodes) >= 32 {
				return errors.New("invalid node ID or node limit")
			}

			g.Nodes = append(g.Nodes, NodeConfig{ID: e.Node})
			index = len(g.Nodes) - 1
		} else if index < 0 {
			return errors.New("configuration node not found")
		}
		if e.Path == RemoveNodePath && e.ValueType == schema.Null {
			g.Nodes = slices.Delete(g.Nodes, index, index+1)
		} else {
			n := &g.Nodes[index]
			switch e.Path {
			case KindPath:
				n.Kind = e.Value.(Kind)
				if outputType(*n) == "" {
					return errors.New("unknown node kind")
				}
			case ModelPath:
				n.Model = e.Value.(string)
			case InputPath:
				n.Input = copyBinding(e.Value.(Binding))
			case InputsPath:
				n.Inputs, _ = copyEvent(*e).Value.(map[string]Binding)
			case PausePath:
				n.Pause = e.Value.(bool)
			case CountPath:
				n.Count = e.Value.(int)
			case SeedPath:
				n.Seed = e.Value.(int64)
			case ParamsPath:
				n.Params, _ = copyEvent(*e).Value.(map[string]string)
			default:
				return fmt.Errorf("unknown node configuration path %q", e.Path)
			}
		}
	}

	s.Graph = g
	return nil
}

func configEvent(node string, path ConfigurationPath, value any) Event {
	typ := configurationType(path)
	switch v := value.(type) {
	case map[string]Binding:
		if v == nil {
			value = nil
		}
	case map[string]string:
		if v == nil {
			value = nil
		}
	}
	if value == nil {
		typ = schema.Null
	}
	return Event{Type: schema.ConfigurationEvent, Node: node, Path: path, ValueType: typ, Value: value}
}

// configurationEvents translates an API configuration document to field writes.
// It never compares execution snapshots or emits graph replacement operations.
func configurationEvents(before, after Graph) []Event {
	var events []Event
	for _, n := range before.Nodes {
		if _, ok := after.Node(n.ID); !ok {
			events = append(events, configEvent(n.ID, RemoveNodePath, nil))
		}
	}

	for _, n := range after.Nodes {
		old, exists := before.Node(n.ID)
		fields := []struct {
			path      ConfigurationPath
			old, next any
		}{
			{KindPath, old.Kind, n.Kind},
			{ModelPath, old.Model, n.Model},
			{InputPath, old.Input, n.Input},
			{InputsPath, old.Inputs, n.Inputs},
			{PausePath, old.Pause, n.Pause},
			{CountPath, old.Count, n.Count},
			{SeedPath, old.Seed, n.Seed},
			{ParamsPath, old.Params, n.Params},
		}

		for _, f := range fields {
			if !exists && f.path == KindPath || !reflect.DeepEqual(f.old, f.next) {
				events = append(events, configEvent(n.ID, f.path, f.next))
			}
		}
	}

	ids := make([]string, 0, len(after.Nodes))
	oldIDs := make([]string, 0, len(before.Nodes))
	for _, n := range after.Nodes {
		ids = append(ids, n.ID)
	}

	for _, n := range before.Nodes {
		oldIDs = append(oldIDs, n.ID)
	}
	if !slices.Equal(ids, oldIDs) {
		events = append(events, configEvent("", OrderPath, ids))
	}
	if before.Primary != after.Primary {
		events = append(events, configEvent("", PrimaryPath, after.Primary))
	}
	if !reflect.DeepEqual(before.Target, after.Target) {
		events = append(events, configEvent("", TargetPath, after.Target))
	}

	return events
}
