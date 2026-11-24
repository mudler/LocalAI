package enginesettings

import (
	"fmt"
	"strings"
)

// Config is a partial model config: the YAML keys of a model file, nested
// maps for blocks such as "parameters", and the "options" list. It is the
// shape a client sends and receives, whatever the engine.
type Config map[string]any

// lookup reads the value at a dotted path. ok is false when the value is
// missing or null; err is set when a block on the way is not a map.
func (c Config) lookup(path string) (v any, ok bool, err error) {
	keys := strings.Split(path, ".")
	var node map[string]any = c
	for i, k := range keys {
		v, present := node[k]
		if !present || v == nil {
			return nil, false, nil
		}
		if i == len(keys)-1 {
			return v, true, nil
		}
		next, isMap := v.(map[string]any)
		if !isMap {
			if c2, isConfig := v.(Config); isConfig {
				next = c2
			} else {
				return nil, false, fmt.Errorf("%s is not a map", strings.Join(keys[:i+1], "."))
			}
		}
		node = next
	}
	return nil, false, nil
}

// set stores a value at a dotted path, creating the blocks on the way.
func (c Config) set(path string, v any) {
	keys := strings.Split(path, ".")
	var node map[string]any = c
	for _, k := range keys[:len(keys)-1] {
		next, isMap := node[k].(map[string]any)
		if !isMap {
			next = map[string]any{}
			node[k] = next
		}
		node = next
	}
	node[keys[len(keys)-1]] = v
}

// unset removes the value at a dotted path.
func (c Config) unset(path string) {
	keys := strings.Split(path, ".")
	var node map[string]any = c
	for _, k := range keys[:len(keys)-1] {
		next, isMap := node[k].(map[string]any)
		if !isMap {
			return
		}
		node = next
	}
	delete(node, keys[len(keys)-1])
}

// options reads the "options" list. Every entry must be a string.
func (c Config) options() ([]string, error) {
	v, ok := c["options"]
	if !ok || v == nil {
		return nil, nil
	}
	switch l := v.(type) {
	case []string:
		return l, nil
	case []any:
		out := make([]string, 0, len(l))
		for _, e := range l {
			s, isStr := e.(string)
			if !isStr {
				return nil, fmt.Errorf("options: %v is not a string", e)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("options is not a list")
}
