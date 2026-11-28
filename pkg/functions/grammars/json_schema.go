package grammars

// a golang port of https://github.com/ggerganov/llama.cpp/pull/1887

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// maxSchemaDepth bounds how deeply visit may recurse into a client-supplied
// schema. A cyclic $ref is caught by refsInProgress, but a deeply nested yet
// acyclic schema (e.g. thousands of nested arrays/objects) could still recurse
// until the goroutine stack is exhausted and crash the whole process. Rejecting
// the request once this depth is exceeded turns that potential crash into an
// ordinary per-request error. The limit is far above any realistic schema.
const (
	maxSchemaDepth       = 256
	maxGrammarRepetition = 2000
)

func schemaBound(schema map[string]any, key string) (int, bool, error) {
	raw, ok := schema[key]
	if !ok {
		return 0, false, nil
	}

	var value int64
	switch n := raw.(type) {
	case int:
		value = int64(n)
	case int32:
		value = int64(n)
	case int64:
		value = n
	case float64:
		if math.Trunc(n) != n || n < 0 || n > maxGrammarRepetition {
			return 0, false, fmt.Errorf("%s must be an integer between 0 and %d", key, maxGrammarRepetition)
		}
		value = int64(n)
	default:
		return 0, false, fmt.Errorf("%s must be an integer between 0 and %d", key, maxGrammarRepetition)
	}

	if value < 0 || value > maxGrammarRepetition {
		return 0, false, fmt.Errorf("%s must be an integer between 0 and %d", key, maxGrammarRepetition)
	}
	return int(value), true, nil
}

func schemaBounds(schema map[string]any, minKey, maxKey string) (int, int, bool, error) {
	minValue, hasMin, err := schemaBound(schema, minKey)
	if err != nil {
		return 0, 0, false, err
	}
	maxValue, hasMax, err := schemaBound(schema, maxKey)
	if err != nil {
		return 0, 0, false, err
	}
	if hasMin && hasMax && minValue > maxValue {
		return 0, 0, false, fmt.Errorf("%s cannot exceed %s", minKey, maxKey)
	}
	if !hasMax {
		maxValue = -1
	}
	return minValue, maxValue, hasMin || hasMax, nil
}

func buildRepetition(item string, minItems, maxItems int, separator string) string {
	if maxItems == 0 {
		return ""
	}
	if minItems == 0 && maxItems == 1 {
		return item + "?"
	}
	if separator == "" {
		switch {
		case minItems == 0 && maxItems < 0:
			return item + "*"
		case minItems == 1 && maxItems < 0:
			return item + "+"
		case maxItems < 0:
			return fmt.Sprintf("%s{%d,}", item, minItems)
		default:
			return fmt.Sprintf("%s{%d,%d}", item, minItems, maxItems)
		}
	}

	maxTail := maxItems
	if maxTail > 0 {
		maxTail--
	}
	minTail := max(0, minItems-1)
	result := item + " " + buildRepetition("("+separator+" "+item+")", minTail, maxTail, "")
	if minItems == 0 {
		return "(" + result + ")?"
	}
	return result
}

type JSONSchemaConverter struct {
	propOrder map[string]int
	rules     Rules
	// refsInProgress tracks the $ref targets currently on the recursion stack
	// so a self- or mutually-referential schema cannot recurse forever. It is
	// pushed before descending into a referenced schema and popped afterwards,
	// so sibling (non-cyclic) reuse of the same $ref is still allowed.
	refsInProgress map[string]bool
	// depth is the current recursion depth of visit, bounded by maxSchemaDepth
	// to guard against stack exhaustion on deeply nested acyclic schemas.
	depth int
}

func NewJSONSchemaConverter(propOrder string) *JSONSchemaConverter {
	propOrderSlice := strings.Split(propOrder, ",")
	propOrderMap := make(map[string]int)
	for idx, name := range propOrderSlice {
		propOrderMap[name] = idx
	}

	rules := make(map[string]string)
	rules["space"] = SPACE_RULE

	return &JSONSchemaConverter{
		propOrder:      propOrderMap,
		rules:          rules,
		refsInProgress: make(map[string]bool),
	}
}

func (sc *JSONSchemaConverter) formatLiteral(literal any) (string, error) {
	jLiteral, err := jsonString(literal)
	if err != nil {
		return "", err
	}
	escaped := GRAMMAR_LITERAL_ESCAPE_RE.ReplaceAllStringFunc(jLiteral, func(match string) string {
		return GRAMMAR_LITERAL_ESCAPES[match]
	})
	return fmt.Sprintf(`"%s"`, escaped), nil
}

func (sc *JSONSchemaConverter) addRule(name, rule string) string {
	escName := INVALID_RULE_CHARS_RE.ReplaceAllString(name, "-")
	key := escName
	if existingRule, ok := sc.rules[escName]; ok && existingRule != rule {
		i := 0
		for {
			key = fmt.Sprintf("%s%d", escName, i)
			if _, ok := sc.rules[key]; !ok {
				break
			}
			i++
		}
	}
	sc.rules[key] = rule
	return key
}

func (sc *JSONSchemaConverter) visit(schema map[string]any, name string, rootSchema map[string]any) (string, error) {
	sc.depth++
	defer func() { sc.depth-- }()
	if sc.depth > maxSchemaDepth {
		return "", fmt.Errorf("schema nesting exceeds maximum depth %d while building grammar", maxSchemaDepth)
	}
	st, existType := schema["type"]
	var schemaType string
	var schemaTypes []string
	if existType {
		// Handle both single type strings and arrays of types (e.g., ["string", "null"])
		switch v := st.(type) {
		case string:
			// Single type: "type": "string"
			schemaType = v
			schemaTypes = []string{v}
		case []any:
			// Multiple types: "type": ["string", "null"]
			for _, item := range v {
				if typeStr, ok := item.(string); ok {
					schemaTypes = append(schemaTypes, typeStr)
				}
			}
			// Use the first type as the primary schema type for compatibility
			if len(schemaTypes) > 0 {
				schemaType = schemaTypes[0]
			}
		}
	}
	ruleName := name
	if name == "" {
		ruleName = "root"
	}
	_, oneOfExists := schema["oneOf"]
	_, anyOfExists := schema["anyOf"]
	if oneOfExists || anyOfExists {
		var alternatives []string
		oneOfSchemas, oneOfExists := schema["oneOf"].([]any)
		anyOfSchemas, anyOfExists := schema["anyOf"].([]any)

		if oneOfExists {
			for i, altSchema := range oneOfSchemas {
				alternative, err := sc.visit(altSchema.(map[string]any), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				if err != nil {
					return "", err
				}
				alternatives = append(alternatives, alternative)
			}
		} else if anyOfExists {
			for i, altSchema := range anyOfSchemas {
				alternative, err := sc.visit(altSchema.(map[string]any), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				if err != nil {
					return "", err
				}
				alternatives = append(alternatives, alternative)
			}
		}

		rule := strings.Join(alternatives, " | ")
		return sc.addRule(ruleName, rule), nil
	} else if ref, exists := schema["$ref"].(string); exists {
		// A client-supplied schema may contain a cyclic $ref (e.g. a $def that
		// references itself directly or through a chain). Without this guard the
		// recursion below never terminates and exhausts the goroutine stack,
		// crashing the whole process rather than just failing the request.
		if sc.refsInProgress[ref] {
			return "", fmt.Errorf("cyclic $ref detected while building grammar: %s", ref)
		}
		referencedSchema, err := sc.resolveReference(ref, rootSchema)
		if err != nil {
			return "", err
		}
		sc.refsInProgress[ref] = true
		result, err := sc.visit(referencedSchema, name, rootSchema)
		delete(sc.refsInProgress, ref)
		return result, err
	} else if constVal, exists := schema["const"]; exists {
		literal, err := sc.formatLiteral((constVal))
		if err != nil {
			return "", err
		}
		return sc.addRule(ruleName, literal), nil
	} else if enumVals, exists := schema["enum"].([]any); exists {
		var enumRules []string
		for _, enumVal := range enumVals {
			enumRule, err := sc.formatLiteral(enumVal)
			if err != nil {
				return "", err
			}
			enumRules = append(enumRules, enumRule)
		}
		rule := strings.Join(enumRules, " | ")
		return sc.addRule(ruleName, rule), nil
	} else if properties, exists := schema["properties"].(map[string]any); schemaType == "object" && exists {
		propOrder := sc.propOrder
		var propPairs []struct {
			propName   string
			propSchema map[string]any
		}

		for propName, propSchema := range properties {
			propPairs = append(propPairs, struct {
				propName   string
				propSchema map[string]any
			}{propName: propName, propSchema: propSchema.(map[string]any)})
		}

		slices.SortFunc(propPairs, func(a, b struct {
			propName   string
			propSchema map[string]any
		}) int {
			// Use presence in the order map (not a non-zero sentinel) so that
			// the first listed key — index 0 — is honored. Keys present in
			// properties_order sort by their index and ahead of any key that
			// isn't listed; unlisted keys keep a stable alphabetical order.
			aOrder, aOK := propOrder[a.propName]
			bOrder, bOK := propOrder[b.propName]
			switch {
			case aOK && bOK:
				return cmp.Compare(aOrder, bOrder)
			case aOK:
				return -1
			case bOK:
				return 1
			default:
				return cmp.Compare(a.propName, b.propName)
			}
		})

		var rule strings.Builder
		rule.WriteString(`"{" space`)

		for i, propPair := range propPairs {
			propName := propPair.propName
			propSchema := propPair.propSchema
			propRuleName, err := sc.visit(propSchema, fmt.Sprintf("%s-%s", ruleName, propName), rootSchema)
			if err != nil {
				return "", err
			}
			lPropName, err := sc.formatLiteral(propName)
			if err != nil {
				return "", err
			}
			if i > 0 {
				rule.WriteString(` "," space`)
			}

			rule.WriteString(fmt.Sprintf(` %s space ":" space %s`, lPropName, propRuleName))
		}

		rule.WriteString(` "}" space`)
		return sc.addRule(ruleName, rule.String()), nil
	} else if items, exists := schema["items"].(map[string]any); schemaType == "array" && exists {
		itemRuleName, err := sc.visit(items, fmt.Sprintf("%s-item", ruleName), rootSchema)
		if err != nil {
			return "", err
		}
		minItems, maxItems, _, err := schemaBounds(schema, "minItems", "maxItems")
		if err != nil {
			return "", err
		}
		itemsRule := buildRepetition(itemRuleName, minItems, maxItems, `"," space`)
		rule := `"[" space`
		if itemsRule != "" {
			rule += " " + itemsRule
		}
		rule += ` "]" space`
		return sc.addRule(ruleName, rule), nil
	} else if properties, _ := schema["properties"].(map[string]any); (schemaType == "object" || schemaType == "") && len(properties) == 0 {
		// Handle empty object schema (no properties)
		rule := `"{" space "}" space`
		return sc.addRule(ruleName, rule), nil
	} else {
		if schemaType == "string" {
			minLength, maxLength, bounded, err := schemaBounds(schema, "minLength", "maxLength")
			if err != nil {
				return "", err
			}
			if bounded {
				charRule := sc.addRule("char", PRIMITIVE_RULES["char"])
				rule := `"\"" ` + buildRepetition(charRule, minLength, maxLength, "") + ` "\"" space`
				return sc.addRule(ruleName, rule), nil
			}
		}
		// Handle primitive types, including multi-type arrays like ["string", "null"]
		if len(schemaTypes) > 1 {
			// Generate a union of multiple primitive types
			var typeRules []string
			for _, t := range schemaTypes {
				primitiveRule, exists := PRIMITIVE_RULES[t]
				if !exists {
					return "", fmt.Errorf("unrecognized type in multi-type schema: %s (schema: %v)", t, schema)
				}
				typeRules = append(typeRules, primitiveRule)
			}
			rule := "(" + strings.Join(typeRules, " | ") + ")"
			return sc.addRule(ruleName, rule), nil
		} else {
			// Single type
			primitiveRule, exists := PRIMITIVE_RULES[schemaType]
			if !exists {
				return "", fmt.Errorf("unrecognized schema: %v (type: %s)", schema, schemaType)
			}
			if ruleName == "root" {
				schemaType = "root"
			}
			return sc.addRule(schemaType, primitiveRule), nil
		}
	}
}
func (sc *JSONSchemaConverter) resolveReference(ref string, rootSchema map[string]any) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/$defs/") {
		return nil, fmt.Errorf("invalid reference format: %s", ref)
	}

	defKey := strings.TrimPrefix(ref, "#/$defs/")
	definitions, exists := rootSchema["$defs"].(map[string]any)
	if !exists {
		return nil, fmt.Errorf("no definitions found in the schema: %s", rootSchema)
	}

	def, exists := definitions[defKey].(map[string]any)
	if !exists {
		return nil, fmt.Errorf("definition not found: %s %+v", defKey, definitions)
	}

	return def, nil
}

func (sc *JSONSchemaConverter) Grammar(schema map[string]any, options ...func(*GrammarOption)) (string, error) {
	sc.addRule("freestring", PRIMITIVE_RULES["freestring"])
	_, err := sc.visit(schema, "", schema)
	if err != nil {
		return "", err
	}
	return sc.rules.ToGrammar(options...), nil
}

func (sc *JSONSchemaConverter) GrammarFromBytes(b []byte, options ...func(*GrammarOption)) (string, error) {
	var schema map[string]any
	err := json.Unmarshal(b, &schema)
	if err != nil {
		return "", err
	}
	return sc.Grammar(schema, options...)
}
