package grammar

// a golang port of https://github.com/ggerganov/llama.cpp/pull/1887

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	SPACE_RULE = `" "?`

	PRIMITIVE_RULES = map[string]string{
		"boolean": `("true" | "false") space`,
		"number":  `("-"? ([0-9] | [1-9] [0-9]*)) ("." [0-9]+)? ([eE] [-+]? [0-9]+)? space`,
		"integer": `("-"? ([0-9] | [1-9] [0-9]*)) space`,
		"string": `"\"" (
			[^"\\] |
			"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
		  )* "\"" space`,
		"null": `"null" space`,
	}

	INVALID_RULE_CHARS_RE     = regexp.MustCompile(`[^a-zA-Z0-9-]+`)
	GRAMMAR_LITERAL_ESCAPE_RE = regexp.MustCompile(`[\r\n"]`)
	GRAMMAR_LITERAL_ESCAPES   = map[string]string{
		"\r": `\r`,
		"\n": `\n`,
		`"`:  `\"`,
	}
)

type JSONSchemaConverter struct {
	propOrder map[string]int
	rules     map[string]string
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

func (sc *JSONSchemaConverter) formatLiteral(literal interface{}) string {
	escaped := GRAMMAR_LITERAL_ESCAPE_RE.ReplaceAllStringFunc(jsonString(literal), func(match string) string {
		return GRAMMAR_LITERAL_ESCAPES[match]
	})
	return fmt.Sprintf(`"%s"`, escaped)
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

func (sc *JSONSchemaConverter) formatGrammar() string {
	var lines []string
	for name, rule := range sc.rules {
		lines = append(lines, fmt.Sprintf("%s ::= %s", name, rule))
	}
	return strings.Join(lines, "\n")
}

func (sc *JSONSchemaConverter) visit(schema map[string]interface{}, name string, rootSchema map[string]interface{}) string {
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
				alternative := sc.visit(altSchema.(map[string]interface{}), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				alternatives = append(alternatives, alternative)
			}
		} else if anyOfExists {
			for i, altSchema := range anyOfSchemas {
				alternative := sc.visit(altSchema.(map[string]interface{}), fmt.Sprintf("%s-%d", ruleName, i), rootSchema)
				alternatives = append(alternatives, alternative)
			}
		}

		rule := strings.Join(alternatives, " | ")
		return sc.addRule(ruleName, rule)
	} else if ref, exists := schema["$ref"].(string); exists {
		referencedSchema := sc.resolveReference(ref, rootSchema)
		return sc.visit(referencedSchema, name, rootSchema)
	} else if constVal, exists := schema["const"]; exists {
		return sc.addRule(ruleName, sc.formatLiteral(constVal))
	} else if enumVals, exists := schema["enum"].([]interface{}); exists {
		var enumRules []string
		for _, enumVal := range enumVals {
			enumRule := sc.formatLiteral(enumVal)
			enumRules = append(enumRules, enumRule)
		}
		rule := strings.Join(enumRules, " | ")
		return sc.addRule(ruleName, rule)
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
			propRuleName := sc.visit(propSchema, fmt.Sprintf("%s-%s", ruleName, propName), rootSchema)

			if i > 0 {
				rule.WriteString(` "," space`)
			}

			rule.WriteString(fmt.Sprintf(` %s space ":" space %s`, sc.formatLiteral(propName), propRuleName))
		}

		rule.WriteString(` "}" space`)
		return sc.addRule(ruleName, rule.String())
	} else if items, exists := schema["items"].(map[string]interface{}); schemaType == "array" && exists {
		itemRuleName := sc.visit(items, fmt.Sprintf("%s-item", ruleName), rootSchema)
		rule := fmt.Sprintf(`"[" space (%s ("," space %s)*)? "]" space`, itemRuleName, itemRuleName)
		return sc.addRule(ruleName, rule)
	} else {
		primitiveRule, exists := PRIMITIVE_RULES[schemaType]
		if !exists {
			panic(fmt.Sprintf("Unrecognized schema: %v", schema))
		}
		if ruleName == "root" {
			schemaType = "root"
		}
		return sc.addRule(schemaType, primitiveRule)
	}
}
func (sc *JSONSchemaConverter) resolveReference(ref string, rootSchema map[string]interface{}) map[string]interface{} {
	if !strings.HasPrefix(ref, "#/$defs/") {
		panic(fmt.Sprintf("Invalid reference format: %s", ref))
	}

	defKey := strings.TrimPrefix(ref, "#/$defs/")
	definitions, exists := rootSchema["$defs"].(map[string]interface{})
	if !exists {
		fmt.Println(rootSchema)

		panic("No definitions found in the schema")
	}

	def, exists := definitions[defKey].(map[string]interface{})
	if !exists {
		fmt.Println(definitions)

		panic(fmt.Sprintf("Definition not found: %s", defKey))
	}

	return def
}
func (sc *JSONSchemaConverter) Grammar(schema map[string]interface{}) string {
	sc.visit(schema, "", schema)
	return sc.formatGrammar()
}

func (sc *JSONSchemaConverter) GrammarFromBytes(b []byte) string {
	var schema map[string]interface{}
	_ = json.Unmarshal(b, &schema)
	return sc.Grammar(schema)
}

func jsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

type FunctionName struct {
	Const string `json:"const"`
}

type Properties struct {
	Function  FunctionName `json:"function"`
	Arguments Argument     `json:"arguments"`
}

type Argument struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type Item struct {
	Type       string     `json:"type"`
	Properties Properties `json:"properties"`
}

type JSONFunctionStructure struct {
	OneOf []Item                 `json:"oneOf,omitempty"`
	AnyOf []Item                 `json:"anyOf,omitempty"`
	Defs  map[string]interface{} `json:"$defs,omitempty"`
}

func (j JSONFunctionStructure) Grammar(propOrder string) string {
	dat, _ := json.Marshal(j)
	return NewJSONSchemaConverter(propOrder).GrammarFromBytes(dat)
}
