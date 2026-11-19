package modelupdates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// leaf is one leaf of a model config: a scalar, a list or an empty map, at a
// path of map keys. Lists are compared and replaced whole.
type leaf struct {
	path  []string
	value any
}

func parseConfig(data []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return m, nil
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func flatten(prefix []string, v any, out map[string]leaf) {
	if m, ok := v.(map[string]any); ok && len(m) > 0 {
		for k, val := range m {
			flatten(append(slices.Clone(prefix), k), val, out)
		}
		return
	}
	if len(prefix) == 0 {
		return
	}
	out[strings.Join(prefix, ".")] = leaf{path: prefix, value: v}
}

func leaves(m map[string]any) map[string]leaf {
	out := map[string]leaf{}
	flatten(nil, m, out)
	return out
}

// sameValue compares two decoded YAML values by their JSON form, so 1 and 1.0
// written by different encoders, or maps in a different order, are equal.
func sameValue(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return bytes.Equal(ja, jb)
}

func isOperatorKey(path []string) bool {
	return len(path) > 0 && slices.Contains(modeladmin.OperatorStateKeys, path[0])
}

// valueAt returns the leaf value at key, nil when absent.
func valueAt(l map[string]leaf, key string) any {
	if v, ok := l[key]; ok {
		return v.value
	}
	return nil
}

// DiffEdits lists the keys of a model config that a person changed: the keys
// whose value on disk (mine) differs from the value the install wrote
// (installed). Operator state (OperatorStateKeys) is never an edit. When
// planned (the config the update would write) is given, an edit is a conflict
// when the update also changes that key.
func DiffEdits(installed, mine, planned []byte) ([]KeyEdit, error) {
	instMap, err := parseConfig(installed)
	if err != nil {
		return nil, fmt.Errorf("parse the installed config: %w", err)
	}
	mineMap, err := parseConfig(mine)
	if err != nil {
		return nil, fmt.Errorf("parse the config on disk: %w", err)
	}
	inst, own := leaves(instMap), leaves(mineMap)
	var gal map[string]leaf
	if planned != nil {
		galMap, err := parseConfig(planned)
		if err != nil {
			return nil, fmt.Errorf("parse the gallery config: %w", err)
		}
		gal = leaves(galMap)
	}
	keys := map[string][]string{}
	for k, l := range inst {
		keys[k] = l.path
	}
	for k, l := range own {
		keys[k] = l.path
	}
	var edits []KeyEdit
	for key, path := range keys {
		if isOperatorKey(path) {
			continue
		}
		iv, mv := valueAt(inst, key), valueAt(own, key)
		_, inInst := inst[key]
		_, inOwn := own[key]
		if inInst == inOwn && sameValue(iv, mv) {
			continue
		}
		e := KeyEdit{Key: key, Installed: iv, Mine: mv}
		if gal != nil {
			gv := valueAt(gal, key)
			_, inGal := gal[key]
			e.Gallery = gv
			e.Conflict = inGal != inInst || !sameValue(gv, iv)
		}
		edits = append(edits, e)
	}
	slices.SortFunc(edits, func(a, b KeyEdit) int { return strings.Compare(a.Key, b.Key) })
	return edits, nil
}

// Conflicts returns the keys of edits that are conflicts.
func Conflicts(edits []KeyEdit) []string {
	var out []string
	for _, e := range edits {
		if e.Conflict {
			out = append(out, e.Key)
		}
	}
	return out
}

// ErrNeedsChoices refuses an upgrade whose update changes keys a person
// edited and that has no choice for them. MergeConfig returns it when the
// choices miss a key; CheckRequest returns it before the upgrade is queued.
type ErrNeedsChoices struct{ Keys []string }

func (e *ErrNeedsChoices) Error() string {
	return "the update changes keys that were edited; choose " + ChoiceMine + " or " + ChoiceGallery + " for each: " + strings.Join(e.Keys, ", ")
}

// MergeConfig builds the config an upgrade writes: the config the update
// would write (planned), with the person's edits applied again. An edit the
// update does not touch is kept. An edit the update also changes needs a
// choice in choices (ChoiceMine or ChoiceGallery, by dotted key); without one
// MergeConfig fails with *ErrNeedsChoices and the upgrade does not
// start. Operator state (pinned, disabled) always keeps the value on disk.
func MergeConfig(installed, mine, planned []byte, choices map[string]string) ([]byte, error) {
	edits, err := DiffEdits(installed, mine, planned)
	if err != nil {
		return nil, err
	}
	out, err := parseConfig(planned)
	if err != nil {
		return nil, err
	}
	mineMap, err := parseConfig(mine)
	if err != nil {
		return nil, err
	}
	own := leaves(mineMap)
	var unresolved []string
	for _, e := range edits {
		if e.Conflict {
			switch choices[e.Key] {
			case ChoiceGallery:
				continue
			case ChoiceMine:
			default:
				unresolved = append(unresolved, e.Key)
				continue
			}
		}
		if l, ok := own[e.Key]; ok {
			setPath(out, l.path, l.value)
		} else {
			deletePath(out, strings.Split(e.Key, "."))
		}
	}
	if len(unresolved) > 0 {
		return nil, &ErrNeedsChoices{Keys: unresolved}
	}
	for _, key := range modeladmin.OperatorStateKeys {
		if v, ok := mineMap[key]; ok {
			out[key] = v
		} else {
			delete(out, key)
		}
	}
	return yaml.Marshal(out)
}

func setPath(m map[string]any, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

func deletePath(m map[string]any, path []string) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	delete(m, path[len(path)-1])
}
