// SPDX-License-Identifier: MIT
package middleware

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
)

// probeBudget checks the serialized size before either extracting text or
// invoking encoding/json (whose Encoder also buffers a complete value). It
// walks caller-owned values without copying strings or allocating map keys.
// Unknown custom marshalers fail closed: their allocation cannot be bounded.
func probeBudget(value any) error {
	remaining := systemone.MaxImageBodyBytes
	if !probeValueBudget(reflect.ValueOf(value), &remaining, 0) {
		return fmt.Errorf("router state exceeds decision image request budget or contains unsupported values")
	}
	return nil
}

const (
	probeMaxDepth   = 100 // Bound recursion for cyclic direct/internal requests.
	jsonNumberBytes = 32  // Upper bound for JSON's built-in numeric representations.
)

func probeSpend(left *int, n int) bool {
	if n > *left {
		return false
	}
	*left -= n
	return true
}

func probeValueBudget(v reflect.Value, left *int, depth int) bool {
	if depth > probeMaxDepth {
		return false
	}
	if !v.IsValid() {
		return probeSpend(left, len("null"))
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return probeSpend(left, len("null"))
		}
		return probeValueBudget(v.Elem(), left, depth+1)
	}
	// Only these concrete schema structs and plain JSON representations are
	// accepted. Do not emulate arbitrary encoding/json field promotion or tags.
	if v.Type() == reflect.TypeFor[*json.RawMessage]() && !v.IsNil() {
		return probeValueBudget(v.Elem(), left, depth+1)
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		if v.IsNil() {
			return probeSpend(left, 4)
		}
		// RawMessage is compacted and HTML-escaped by encoding/json. Count raw
		// whitespace too; this intentionally overestimates, without executing it.
		if v.Len() > *left {
			return false
		}
		for _, b := range v.Bytes() {
			n := 1
			if b == '<' || b == '>' || b == '&' || b >= 0x80 {
				n = 6
			}
			if !probeSpend(left, n) {
				return false
			}
		}
		return true
	}
	typ := v.Type()
	if typ.Implements(reflect.TypeFor[json.Marshaler]()) || typ.Implements(reflect.TypeFor[encoding.TextMarshaler]()) ||
		reflect.PointerTo(typ).Implements(reflect.TypeFor[json.Marshaler]()) || reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
		return false
	}
	if typ.Name() != "" && typ.PkgPath() != "" && !probeSchemaType(typ) && typ != reflect.TypeFor[json.Number]() {
		return false
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return probeSpend(left, len("null"))
		}
		return probeValueBudget(v.Elem(), left, depth+1)
	case reflect.String:
		return systemone.SpendJSONString(v.String(), left)
	case reflect.Bool:
		return probeSpend(left, len("false"))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return probeSpend(left, jsonNumberBytes)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return probeSpend(left, len("null"))
		}
		// encoding/json base64-encodes byte slices.
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Type().Elem() == reflect.TypeFor[byte]() && v.Len() <= *left && probeSpend(left, 2+4*((v.Len()+2)/3))
		}
		if !probeSpend(left, 2) || v.Len() > *left {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if i > 0 && !probeSpend(left, 1) {
				return false
			}
			if !probeValueBudget(v.Index(i), left, depth+1) {
				return false
			}
		}
		return true
	case reflect.Map:
		if v.IsNil() {
			return probeSpend(left, len("null"))
		}
		if v.Type().Key() != reflect.TypeFor[string]() || !probeSpend(left, 2) || v.Len() > *left {
			return false
		}
		iter := v.MapRange()
		first := true
		for iter.Next() {
			if !first && !probeSpend(left, 1) {
				return false
			}
			first = false
			if !systemone.SpendJSONString(iter.Key().String(), left) || !probeSpend(left, 1) || !probeValueBudget(iter.Value(), left, depth+1) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if !probeSchemaType(v.Type()) {
			return false
		}
		if !probeSpend(left, 2) {
			return false
		}
		first := true
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			name, opts, _ := strings.Cut(tag, ",")
			if field.Anonymous || strings.Contains(opts, "string") {
				return false
			}
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fv := v.Field(i)
			if strings.Contains(opts, "omitempty") && probeEmpty(fv) {
				continue
			}
			if !first && !probeSpend(left, 1) {
				return false
			}
			first = false
			if !systemone.SpendJSONString(name, left) || !probeSpend(left, 1) || !probeValueBudget(fv, left, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func probeEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

func probeSchemaType(t reflect.Type) bool {
	switch t {
	case reflect.TypeFor[schema.Message](), reflect.TypeFor[schema.Messages](),
		reflect.TypeFor[schema.Content](), reflect.TypeFor[schema.ContentURL](), reflect.TypeFor[schema.InputAudio](),
		reflect.TypeFor[schema.ToolCall](), reflect.TypeFor[schema.FunctionCall](),
		reflect.TypeFor[schema.AnthropicMessage](), reflect.TypeFor[schema.AnthropicContentBlock](), reflect.TypeFor[schema.AnthropicImageSource]():
		return true
	}
	return false
}
