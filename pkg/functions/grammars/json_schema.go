package grammars

// a golang port of https://github.com/ggerganov/llama.cpp/pull/1887

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type JSONSchemaConverter struct {
	propOrder map[string]int
	rules     Rules
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
		propOrder: propOrderMap,
		rules:     rules,
	}
}

func (sc *JSONSchemaConverter) formatLiteral(literal interface{}) (string, error) {
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

func (sc *JSONSchemaConverter) visit(schema map[string]interface{}, name string, rootSchema map[string]interface{}) (string, error) {
	st, existType := schema["type"]
	var schemaType string
	if existType {
		schemaType = st.(string)
	}
	ruleName := name
	if name == "" {
		ruleName = "root"
	}
	_, oneOfExists := schema["oneOf"]
	_, anyOfExists := schema["anyOf"]
	if oneOfExists || anyOfExists {
		var alternatives []string
		oneOfSchemas, oneOfExists := schema["oneOf"].([]interface{})
		anyOfSchemas, anyOfExists := schema["anyOf"].([]interface{})

		if oneOfExists {
			for i, altSchema := range oneOfSchemas {
				alternative, err := sc.visit(altSchema.(map[string]interface{}), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				if err != nil {
					return "", err
				}
				alternatives = append(alternatives, alternative)
			}
		} else if anyOfExists {
			for i, altSchema := range anyOfSchemas {
				alternative, err := sc.visit(altSchema.(map[string]interface{}), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				if err != nil {
					return "", err
				}
				alternatives = append(alternatives, alternative)
			}
		}

		rule := strings.Join(alternatives, " | ")
		return sc.addRule(ruleName, rule), nil
	} else if ref, exists := schema["$ref"].(string); exists {
		referencedSchema, err := sc.resolveReference(ref, rootSchema)
		if err != nil {
			return "", err
		}
		return sc.visit(referencedSchema, name, rootSchema)
	} else if constVal, exists := schema["const"]; exists {
		literal, err := sc.formatLiteral((constVal))
		if err != nil {
			return "", err
		}
		return sc.addRule(ruleName, literal), nil
	} else if enumVals, exists := schema["enum"].([]interface{}); exists {
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
	} else if properties, exists := schema["properties"].(map[string]interface{}); schemaType == "object" && exists {
		propOrder := sc.propOrder
		var propPairs []struct {
			propName   string
			propSchema map[string]interface{}
		}

		for propName, propSchema := range properties {
			propPairs = append(propPairs, struct {
				propName   string
				propSchema map[string]interface{}
			}{propName: propName, propSchema: propSchema.(map[string]interface{})})
		}

		sort.Slice(propPairs, func(i, j int) bool {
			iOrder := propOrder[propPairs[i].propName]
			jOrder := propOrder[propPairs[j].propName]
			if iOrder != 0 && jOrder != 0 {
				return iOrder < jOrder
			}
			return propPairs[i].propName < propPairs[j].propName
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
	} else if items, exists := schema["items"].(map[string]interface{}); schemaType == "array" && exists {
		itemRuleName, err := sc.visit(items, fmt.Sprintf("%s-item", ruleName), rootSchema)
		if err != nil {
			return "", err
		}
		rule := fmt.Sprintf(`"[" space (%s ("," space %s)*)? "]" space`, itemRuleName, itemRuleName)
		return sc.addRule(ruleName, rule), nil
	} else {
		primitiveRule, exists := PRIMITIVE_RULES[schemaType]
		if !exists {
			return "", fmt.Errorf("unrecognized schema: %v", schema)
		}
		if ruleName == "root" {
			schemaType = "root"
		}
		return sc.addRule(schemaType, primitiveRule), nil
	}
}
func (sc *JSONSchemaConverter) resolveReference(ref string, rootSchema map[string]interface{}) (map[string]interface{}, error) {
	if !strings.HasPrefix(ref, "#/$defs/") {
		return nil, fmt.Errorf("invalid reference format: %s", ref)
	}

	defKey := strings.TrimPrefix(ref, "#/$defs/")
	definitions, exists := rootSchema["$defs"].(map[string]interface{})
	if !exists {
		return nil, fmt.Errorf("no definitions found in the schema: %s", rootSchema)
	}

	def, exists := definitions[defKey].(map[string]interface{})
	if !exists {
		return nil, fmt.Errorf("definition not found: %s %+v", defKey, definitions)
	}

	return def, nil
}

func (sc *JSONSchemaConverter) Grammar(schema map[string]interface{}, options ...func(*GrammarOption)) (string, error) {
	sc.addRule("freestring", PRIMITIVE_RULES["freestring"])
	_, err := sc.visit(schema, "", schema)
	if err != nil {
		return "", err
	}
	return sc.rules.ToGrammar(options...), nil
}

func (sc *JSONSchemaConverter) GrammarFromBytes(b []byte, options ...func(*GrammarOption)) (string, error) {
	var schema map[string]interface{}
	err := json.Unmarshal(b, &schema)
	if err != nil {
		return "", err
	}
	return sc.Grammar(schema, options...)
}
