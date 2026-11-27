package grammars

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrLLGuidanceUnsupported is returned when the grammar options need a GBNF
// feature that the llguidance form does not reproduce. The caller keeps the
// GBNF grammar.
var ErrLLGuidanceUnsupported = errors.New("grammar options are not supported by the llguidance form")

// llguidanceJSONOptions are the x-guidance options of the schema. The
// whitespace between two JSON tokens is one optional space, as the space rule
// of the GBNF grammar allows, so the model writes the same JSON with both
// engines; free whitespace would also let a small model loop on newlines.
// coerce_one_of treats oneOf as anyOf, as the GBNF grammar does. lenient
// skips the keywords llguidance does not support instead of failing.
var llguidanceJSONOptions = map[string]any{
	"whitespace_pattern": `\x20`,
	"item_separator":     ",",
	"key_separator":      ":",
	"coerce_one_of":      true,
	"lenient":            true,
}

// LLGuidanceGrammar returns the llguidance form (Lark with a %json rule) of
// the grammar that JSONSchemaConverter builds for schema with the same
// options. llama.cpp built with llguidance sends a grammar that starts with
// %llguidance to llguidance instead of its GBNF sampler.
//
// The GBNF converter requires every property of an object and allows no
// other one. The schema is normalized the same way where it does not say
// otherwise: an object without "required" requires all its properties, and an
// object without "additionalProperties" allows no others.
func LLGuidanceGrammar(schema map[string]any, options ...func(*GrammarOption)) (string, error) {
	opts := &GrammarOption{}
	opts.Apply(options...)

	// Mixed mode (free text or JSON) and the llama 3.1 function syntax are
	// not JSON, so they stay GBNF.
	if opts.MaybeString || opts.SchemaType == LLama31Schema {
		return "", ErrLLGuidanceUnsupported
	}

	normalized, ok := normalizeLLGuidanceSchema(schema).(map[string]any)
	if !ok {
		return "", fmt.Errorf("schema is not an object")
	}
	normalized["x-guidance"] = llguidanceJSONOptions

	var buf bytes.Buffer
	if err := writeOrderedJSON(&buf, normalized, propOrderIndex(opts.PropOrder), false); err != nil {
		return "", err
	}
	jsonRule := "%json " + buf.String()

	if opts.Prefix == "" && !opts.MaybeArray {
		return "%llguidance {}\nstart: " + jsonRule + "\n", nil
	}

	root := "value"
	if opts.MaybeArray {
		root = "arr | value"
	}
	if opts.Prefix != "" {
		prefix, err := larkString(opts.Prefix)
		if err != nil {
			return "", err
		}
		root = prefix + " (" + root + ")"
	}

	var g strings.Builder
	g.WriteString("%llguidance {}\n")
	g.WriteString("start: " + root + "\n")
	g.WriteString("value: " + jsonRule + "\n")
	if opts.MaybeArray {
		// The same array of calls as the GBNF arr rule.
		if opts.DisableParallelNewLines {
			g.WriteString(`arr: "[" (value ("," value)*)? "]"` + "\n")
		} else {
			g.WriteString(`arr: "[\n" (value (",\n" value)*)? "]"` + "\n")
		}
	}
	return g.String(), nil
}

// normalizeLLGuidanceSchema returns a copy of v in which every object schema
// that does not say otherwise requires all its properties and allows no other.
func normalizeLLGuidanceSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t)+2)
		for k, val := range t {
			switch k {
			case "properties", "$defs", "definitions", "patternProperties":
				// Maps of name to schema.
				if m, ok := val.(map[string]any); ok {
					mm := make(map[string]any, len(m))
					for name, s := range m {
						mm[name] = normalizeLLGuidanceSchema(s)
					}
					out[k] = mm
					continue
				}
			case "items", "additionalProperties", "not", "if", "then", "else", "contains":
				out[k] = normalizeLLGuidanceSchema(val)
				continue
			case "anyOf", "oneOf", "allOf", "prefixItems":
				if l, ok := val.([]any); ok {
					ll := make([]any, len(l))
					for i, s := range l {
						ll[i] = normalizeLLGuidanceSchema(s)
					}
					out[k] = ll
					continue
				}
			}
			out[k] = val
		}
		if props, ok := out["properties"].(map[string]any); ok {
			if _, has := out["required"]; !has {
				names := make([]any, 0, len(props))
				for _, name := range sortedKeys(props) {
					names = append(names, name)
				}
				out["required"] = names
			}
			if _, has := out["additionalProperties"]; !has {
				out["additionalProperties"] = false
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = normalizeLLGuidanceSchema(s)
		}
		return out
	}
	return v
}

func propOrderIndex(propOrder string) map[string]int {
	order := map[string]int{}
	if propOrder == "" {
		return order
	}
	for i, k := range strings.Split(propOrder, ",") {
		order[k] = i
	}
	return order
}

// writeOrderedJSON writes v as JSON. llguidance generates the properties of an
// object in the order of the schema, so the keys of a "properties" map follow
// the property order of the grammar options, then the alphabetical order, as
// in the GBNF converter. Other maps are written sorted, as encoding/json does.
func writeOrderedJSON(buf *bytes.Buffer, v any, order map[string]int, isProperties bool) error {
	switch t := v.(type) {
	case map[string]any:
		keys := sortedKeys(t)
		if isProperties {
			slices.SortStableFunc(keys, func(a, b string) int {
				ai, aok := order[a]
				bi, bok := order[b]
				switch {
				case aok && bok:
					return cmp.Compare(ai, bi)
				case aok:
					return -1
				case bok:
					return 1
				}
				return cmp.Compare(a, b)
			})
		}
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSONValue(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeOrderedJSON(buf, t[k], order, !isProperties && k == "properties"); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeOrderedJSON(buf, e, order, false); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	}
	return writeJSONValue(buf, v)
}

// writeJSONValue writes a scalar without the HTML escaping of encoding/json,
// so the schema reads the same in the grammar as in the request.
func writeJSONValue(buf *bytes.Buffer, v any) error {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	// Encode adds a newline.
	buf.Truncate(buf.Len() - 1)
	return nil
}

// larkString quotes s as a Lark string literal, which takes JSON escapes.
func larkString(s string) (string, error) {
	var buf bytes.Buffer
	if err := writeJSONValue(&buf, s); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
