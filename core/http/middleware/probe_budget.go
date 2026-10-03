// SPDX-License-Identifier: MIT
package middleware

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

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
	jsonEscapeBytes = len(`\u0000`)
	jsonNumberBytes = 32 // Upper bound for JSON's built-in numeric representations.
)

func probeSpend(left *int, n int) bool {
	if n > *left {
		return false
	}
	*left -= n
	return true
}

func probeStringBudget(s string, left *int) bool {
	if !probeSpend(left, 2) || len(s) > *left {
		return false
	}
	for i := 0; i < len(s); {
		c := s[i]
		n := 1
		switch {
		case c == '"' || c == '\\':
			n = 2
		case c == '\n' || c == '\r' || c == '\t' || c == '\b' || c == '\f':
			n = 2
		case c < 0x20 || c == '<' || c == '>' || c == '&':
			n = jsonEscapeBytes
		case c >= utf8.RuneSelf:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				n = jsonEscapeBytes
			} else if r == '\u2028' || r == '\u2029' {
				n = jsonEscapeBytes
				i += size - 1
			} else {
				n = size
				i += size - 1
			}
		}
		if !probeSpend(left, n) {
			return false
		}
		i++
	}
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
	if v.CanInterface() {
		if _, ok := v.Interface().(encoding.TextMarshaler); ok {
			return false
		}
		if _, ok := v.Interface().(json.Marshaler); ok {
			return false
		}
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return probeSpend(left, len("null"))
		}
		return probeValueBudget(v.Elem(), left, depth+1)
	case reflect.String:
		return probeStringBudget(v.String(), left)
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
			return v.Len() <= *left && probeSpend(left, 2+4*((v.Len()+2)/3))
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
		if v.Type().Key().Kind() != reflect.String || !probeSpend(left, 2) || v.Len() > *left {
			return false
		}
		iter := v.MapRange()
		first := true
		for iter.Next() {
			if !first && !probeSpend(left, 1) {
				return false
			}
			first = false
			if !probeStringBudget(iter.Key().String(), left) || !probeSpend(left, 1) || !probeValueBudget(iter.Value(), left, depth+1) {
				return false
			}
		}
		return true
	case reflect.Struct:
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
			if !probeStringBudget(name, left) || !probeSpend(left, 1) || !probeValueBudget(fv, left, depth+1) {
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
