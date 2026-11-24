package meta

import (
	"reflect"
	"strings"
	"unicode"
)

// WalkModelConfig uses reflection to discover all exported, YAML-tagged fields
// in the given struct type (expected to be config.ModelConfig) and returns a
// slice of FieldMeta with sensible defaults derived from the type information.
func WalkModelConfig(t reflect.Type) []FieldMeta {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var fields []FieldMeta
	walkStruct(t, "", &fields)
	return fields
}

// walkStruct recursively walks a struct type, collecting FieldMeta entries.
// prefix is the dot-path prefix for nested structs (e.g. "function.grammar.").
func walkStruct(t reflect.Type, prefix string, out *[]FieldMeta) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	for sf := range t.Fields() {
		if !sf.IsExported() {
			continue
		}

		yamlTag := sf.Tag.Get("yaml")
		if yamlTag == "-" {
			continue
		}

		yamlKey, opts := parseTag(yamlTag)

		// Handle inline embedding (e.g. LLMConfig `yaml:",inline"`)
		if opts.contains("inline") {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				walkStruct(ft, prefix, out)
			}
			continue
		}

		// If no yaml key and it's an embedded struct without inline, skip unknown pattern
		if yamlKey == "" {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			// Anonymous struct without yaml tag - treat as inline
			if sf.Anonymous && ft.Kind() == reflect.Struct {
				walkStruct(ft, prefix, out)
				continue
			}
			// Named field without yaml tag - skip
			continue
		}

		ft := sf.Type
		isPtr := ft.Kind() == reflect.Pointer
		if isPtr {
			ft = ft.Elem()
		}

		// Named nested struct (not a special type) -> recurse with prefix
		if ft.Kind() == reflect.Struct && !isSpecialType(ft) {
			nestedPrefix := prefix + yamlKey + "."
			walkStruct(ft, nestedPrefix, out)
			continue
		}

		// Leaf field
		path := prefix + yamlKey
		goType := sf.Type.String()
		uiType, component := inferUIType(sf.Type)
		section := inferSection(prefix)
		label := labelFromKey(yamlKey)

		*out = append(*out, FieldMeta{
			Path:      path,
			YAMLKey:   yamlKey,
			GoType:    goType,
			UIType:    uiType,
			Pointer:   isPtr,
			Section:   section,
			Label:     label,
			Component: component,
			Order:     len(*out),
		})
	}
}

// isSpecialType returns true for struct types that should be treated as leaf
// values rather than recursed into (e.g. custom JSON marshalers).
func isSpecialType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	name := t.Name()
	// LogprobsValue, URI types are leaf values despite being structs
	switch name {
	case "LogprobsValue", "URI":
		return true
	}
	return false
}

// inferUIType maps a Go reflect.Type to a UI type string and default component.
func inferUIType(t reflect.Type) (uiType, component string) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Bool:
		return "bool", "toggle"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int", "number"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int", "number"
	case reflect.Float32, reflect.Float64:
		return "float", "number"
	case reflect.String:
		return "string", "input"
	case reflect.Slice:
		elem := t.Elem()
		if elem.Kind() == reflect.String {
			return "[]string", "string-list"
		}
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			return "[]object", "json-editor"
		}
		return "[]any", "json-editor"
	case reflect.Map:
		return "map", "map-editor"
	case reflect.Struct:
		// Special types treated as leaves
		if isSpecialType(t) {
			return "bool", "toggle" // LogprobsValue
		}
		return "object", "json-editor"
	default:
		return "any", "input"
	}
}

// inferSection determines the config section from the dot-path prefix.
func inferSection(prefix string) string {
	if prefix == "" {
		return "general"
	}
	// Remove trailing dot
	p := strings.TrimSuffix(prefix, ".")

	// Use the top-level prefix to determine section
	parts := strings.SplitN(p, ".", 2)
	top := parts[0]

	switch top {
	case "parameters":
		return "parameters"
	case "template":
		return "templates"
	case "function":
		return "functions"
	case "reasoning":
		return "reasoning"
	case "diffusers":
		return "diffusers"
	case "tts":
		return "tts"
	case "pipeline":
		return "pipeline"
	case "grpc":
		return "grpc"
	case "agent":
		return "agent"
	case "mcp":
		return "mcp"
	case "feature_flags":
		return "other"
	case "limit_mm_per_prompt":
		return "llm"
	default:
		return "other"
	}
}

// labelFromKey converts a yaml key like "context_size" to "Context Size".
func labelFromKey(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if len(p) > 0 {
			runes := []rune(p)
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}
	return strings.Join(parts, " ")
}

// tagOptions is a set of comma-separated yaml tag options.
type tagOptions string

func (o tagOptions) contains(optName string) bool {
	s := string(o)
	for s != "" {
		var name string
		if name, s, _ = strings.Cut(s, ","); name == optName {
			return true
		}
	}
	return false
}

// parseTag splits a yaml struct tag into the key name and options.
func parseTag(tag string) (string, tagOptions) {
	if tag == "" {
		return "", ""
	}
	before, after, found := strings.Cut(tag, ",")
	if found {
		return before, tagOptions(after)
	}
	return tag, ""
}

