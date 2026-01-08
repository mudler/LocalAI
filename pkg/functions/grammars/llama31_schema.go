package grammars

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type LLama31SchemaConverter struct {
	fnName string
	rules  Rules
}

func NewLLama31SchemaConverter(fnName string) *LLama31SchemaConverter {
	rules := make(map[string]string)
	rules["space"] = SPACE_RULE
	if fnName == "" {
		fnName = "name"
	}

	return &LLama31SchemaConverter{
		rules:  rules,
		fnName: fnName,
	}
}

var GRAMMAR_LITERAL_ESCAPESLlama = map[string]string{
	"\r": `\r`,
	"\n": `\n`,
}

var GRAMMAR_LITERAL_ESCAPE_RELlama = regexp.MustCompile(`[\r\n]`)

func (sc *LLama31SchemaConverter) formatLiteral(literal interface{}) (string, error) {
	jLiteral, err := jsonString(literal)
	if err != nil {
		return "", err
	}
	escaped := GRAMMAR_LITERAL_ESCAPE_RELlama.ReplaceAllStringFunc(jLiteral, func(match string) string {
		return GRAMMAR_LITERAL_ESCAPESLlama[match]
	})
	return escaped, nil
}

func (sc *LLama31SchemaConverter) formatLiteralQuoted(literal interface{}) (string, error) {
	jLiteral, err := jsonString(literal)
	if err != nil {
		return "", err
	}
	escaped := GRAMMAR_LITERAL_ESCAPE_RE.ReplaceAllStringFunc(jLiteral, func(match string) string {
		return GRAMMAR_LITERAL_ESCAPES[match]
	})
	return fmt.Sprintf(`"%s"`, escaped), nil
}

func (sc *LLama31SchemaConverter) addRule(name, rule string) string {
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

func (sc *LLama31SchemaConverter) visit(schema map[string]interface{}, name string, rootSchema map[string]interface{}) (string, error) {
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
			enumRule, err := sc.formatLiteralQuoted(enumVal)
			if err != nil {
				return "", err
			}
			enumRules = append(enumRules, enumRule)
		}
		rule := strings.Join(enumRules, " | ")
		return sc.addRule(ruleName, rule), nil
	} else if properties, exists := schema["properties"].(map[string]interface{}); schemaType == "object" && exists {
		baseProperty := false
		depth := strings.Split(name, "-")
		if len(depth) == 2 {
			baseProperty = true
		}
		type propData []struct {
			propName   string
			propSchema map[string]interface{}
		}
		var propPairs propData

		for propName, propSchema := range properties {
			propPairs = append(propPairs, struct {
				propName   string
				propSchema map[string]interface{}
			}{propName: propName, propSchema: propSchema.(map[string]interface{})})
		}

		sort.Slice(propPairs, func(i, j int) bool {
			return propPairs[i].propName < propPairs[j].propName
		})

		var rule strings.Builder
		if baseProperty {
			rule.WriteString(`"<function="`)
		} else {
			rule.WriteString(`"{" space`)
		}

		if baseProperty {

			namePair := propData{}
			for i, propPair := range propPairs {
				propName := propPair.propName
				if propName == sc.fnName {
					namePair = append(namePair, propPair)
					// remove namePair from propPairs
					propPairs = append(propPairs[:i], propPairs[i+1:]...)
					break
				}
			}
			if len(namePair) == 0 {
				return "", fmt.Errorf("no function name found in the schema: %s", schema)
			}

			propRuleName, err := sc.visit(namePair[0].propSchema, fmt.Sprintf("%s-%s", ruleName, sc.fnName), rootSchema)
			if err != nil {
				return "", err
			}

			rule.WriteString(fmt.Sprintf(` %s ">{" `, propRuleName))

			for _, propPair := range propPairs {
				propName := propPair.propName
				propSchema := propPair.propSchema
				propRuleName, err := sc.visit(propSchema, fmt.Sprintf("%s-%s", ruleName, propName), rootSchema)
				if err != nil {
					return "", err
				}

				rule.WriteString(propRuleName)
			}

			rule.WriteString(` "}</function>"`)

		} else {
			for i, propPair := range propPairs {
				propName := propPair.propName
				propSchema := propPair.propSchema
				propRuleName, err := sc.visit(propSchema, fmt.Sprintf("%s-%s", ruleName, propName), rootSchema)
				if err != nil {
					return "", err
				}
				lPropName, err := sc.formatLiteralQuoted(propName)
				if err != nil {
					return "", err
				}
				if i > 0 {
					rule.WriteString(` "," space`)
				}

				rule.WriteString(fmt.Sprintf(` %s space ":" space %s`, lPropName, propRuleName))
			}

		}

		if !baseProperty {
			rule.WriteString(` "}" space`)
		}

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
func (sc *LLama31SchemaConverter) resolveReference(ref string, rootSchema map[string]interface{}) (map[string]interface{}, error) {
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

func (sc *LLama31SchemaConverter) Grammar(schema map[string]interface{}, options ...func(*GrammarOption)) (string, error) {
	sc.addRule("freestring", PRIMITIVE_RULES["freestring"])
	_, err := sc.visit(schema, "", schema)
	if err != nil {
		return "", err
	}
	return sc.rules.ToGrammar(options...), nil
}

func (sc *LLama31SchemaConverter) GrammarFromBytes(b []byte, options ...func(*GrammarOption)) (string, error) {
	var schema map[string]interface{}
	err := json.Unmarshal(b, &schema)
	if err != nil {
		return "", err
	}
	return sc.Grammar(schema, options...)
}
