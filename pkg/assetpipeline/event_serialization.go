// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

func argumentsDocument(a Arguments) schema.Arguments {
	return schema.Arguments{
		Node:   nodeDocument(a.Node),
		Input:  valueDocument(a.Input),
		Inputs: convertMap(a.Inputs, valueDocument),
		Target: schema.Profile(a.Target),
	}
}

func restoreArguments(a schema.Arguments) Arguments {
	return Arguments{
		Node:   restoreNode(a.Node),
		Input:  restoreValue(a.Input),
		Inputs: convertMap(a.Inputs, restoreValue),
		Target: Profile(a.Target),
	}
}

func attemptDocument(a *Attempt) *schema.Attempt {
	if a == nil {
		return nil
	}
	return &schema.Attempt{
		ID:          a.ID,
		Arguments:   argumentsDocument(a.Arguments),
		Status:      a.Status,
		Started:     a.Started,
		Finished:    a.Finished,
		Outputs:     valuesDocument(a.Outputs),
		Explanation: a.Explanation,
		Error:       a.Failure.Error(),
		Failure:     failureDocument(a.Failure),
		Progress:    a.Progress,
		JobID:       a.JobID,
		Diagnostics: a.Diagnostics,
	}
}

// The wire format is kept here so replay never needs to parse configuration JSON.
func (e Event) MarshalJSON() ([]byte, error) {
	d := schema.Event{
		ID:        e.ID,
		At:        e.At,
		Version:   e.Version,
		Type:      e.Type,
		Kind:      string(e.Kind),
		Node:      e.Node,
		Message:   e.Message,
		ValueType: e.ValueType,
		Action:    e.Action,
		AttemptID: e.AttemptID,
		Status:    e.Status,
		Outputs:   valuesDocument(e.Outputs),

		Explanation: e.Explanation,
		Failure:     failureDocument(e.Failure),
		Diagnostics: e.Diagnostics,
		Progress:    e.Progress,
		JobID:       e.JobID,
		RunAll:      e.RunAll,
		Path:        string(e.Path),
	}
	if e.Selection != nil {
		v := valueDocument(*e.Selection)
		d.Selection = &v
	}
	if e.Arguments != nil {
		a := argumentsDocument(*e.Arguments)
		d.Arguments = &a
	}
	if e.Type == schema.ConfigurationEvent || e.Type == schema.SystemEvent || e.Value != nil {
		raw, err := json.Marshal(e.Value)
		if err != nil {
			return nil, err
		}
		d.Value = raw
	}
	return json.Marshal(d)
}

func (e *Event) UnmarshalJSON(raw []byte) error {
	var d schema.Event
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	out := Event{
		ID:        d.ID,
		At:        d.At,
		Version:   d.Version,
		Type:      d.Type,
		Kind:      EventKind(d.Kind),
		Node:      d.Node,
		Message:   d.Message,
		ValueType: d.ValueType,
		Action:    d.Action,
		AttemptID: d.AttemptID,
		Status:    d.Status,
		Outputs:   restoreValues(d.Outputs),

		Explanation: d.Explanation,
		Failure:     restoreFailure(d.Failure),
		Diagnostics: d.Diagnostics,
		Progress:    d.Progress,
		JobID:       d.JobID,
		RunAll:      d.RunAll,
		Path:        ConfigurationPath(d.Path),
	}
	if d.Selection != nil {
		v := restoreValue(*d.Selection)
		out.Selection = &v
	}
	if d.Arguments != nil {
		a := restoreArguments(*d.Arguments)
		out.Arguments = &a
	}
	if d.Type == schema.ConfigurationEvent {
		value, err := decodeConfiguration(out.Path, d.ValueType, d.Value)
		if err != nil {
			return err
		}
		out.Value = value
	} else if d.Type == schema.SystemEvent {
		value, err := decodeObservation(d.ValueType, d.Value)
		if err != nil {
			return err
		}
		out.Value = value
	} else if d.Value != nil {
		return fmt.Errorf("configuration value in execution event")
	}
	*e = out
	return nil
}

func decodeStrict(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func decodeConfiguration(path ConfigurationPath, typ schema.DataType, raw json.RawMessage) (any, error) {
	// Null is an explicit removal/reset operation, never a zero-valued scalar.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if typ == schema.Null && (path == InputsPath || path == ParamsPath || path == RemoveNodePath) {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected null for configuration path %q", path)
	}
	if typ != configurationType(path) {
		return nil, fmt.Errorf("invalid type for configuration path %q", path)
	}
	switch path {
	case KindPath:
		return decodeValue[Kind](raw)
	case ModelPath, PrimaryPath:
		return decodeValue[string](raw)
	case InputPath:
		return decodeValue[Binding](raw)
	case InputsPath:
		return decodeValue[map[string]Binding](raw)
	case ParamsPath:
		return decodeValue[map[string]string](raw)
	case PausePath:
		return decodeValue[bool](raw)
	case CountPath:
		return decodeValue[int](raw)
	case SeedPath:
		return decodeValue[int64](raw)
	case OrderPath:
		return decodeValue[[]string](raw)
	case TargetPath:
		return decodeValue[Profile](raw)
	default:
		return nil, fmt.Errorf("unknown configuration path %q", path)
	}
}

func decodeValue[T any](raw []byte) (T, error) {
	var value T
	err := decodeStrict(raw, &value)
	return value, err
}

func validateData(typ schema.DataType, raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	valid := false
	switch typ {
	case schema.String:
		_, valid = value.(string)
	case schema.Boolean:
		_, valid = value.(bool)
	case schema.Integer:
		if n, ok := value.(json.Number); ok {
			_, err := n.Int64()
			valid = err == nil
		}
	case schema.Object:
		_, valid = value.(map[string]any)
	case schema.Array:
		_, valid = value.([]any)
	case schema.Null:
		valid = value == nil
	}
	if !valid {
		return fmt.Errorf("value is not %s", typ)
	}
	return nil
}

func (n NodeState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Stale    bool            `json:"stale,omitempty"`
		Attempt  *schema.Attempt `json:"attempt,omitempty"`
		Selected *Value          `json:"selected,omitempty"`
		Status   Status          `json:"status"`
	}{n.Stale, attemptDocument(n.Attempt), n.Selected, n.Status()})
}

func failureDocument(f *Failure) *schema.Failure {
	if f == nil {
		return nil
	}
	return &schema.Failure{Code: f.Code, Message: f.Message, Cause: failureDocument(f.Cause)}
}

func restoreFailure(f *schema.Failure) *Failure {
	if f == nil {
		return nil
	}
	return &Failure{Code: f.Code, Message: f.Message, Cause: restoreFailure(f.Cause)}
}

func decodeObservation(typ schema.DataType, raw []byte) (any, error) {
	if err := validateData(typ, raw); err != nil {
		return nil, err
	}
	switch typ {
	case schema.String:
		return decodeValue[string](raw)
	case schema.Boolean:
		return decodeValue[bool](raw)
	case schema.Integer:
		return decodeValue[int64](raw)
	case schema.Object, schema.Array:
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		err := decoder.Decode(&value)
		return value, err
	default:
		return nil, nil
	}
}
