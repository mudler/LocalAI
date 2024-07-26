package grammars

// a golang port of https://github.com/ggerganov/llama.cpp/pull/1887

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/pkg/utils"
)

type LLama31SchemaConverter struct {
	fnName string
	rules  map[string]string
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
	fmt.Printf("Adding rule: %s -> %s\n", key, rule)
	return key
}

func (sc *LLama31SchemaConverter) finalizeGrammar(options ...func(*GrammarOption)) string {

	grammarOpts := &GrammarOption{}
	grammarOpts.Apply(options...)

	prefix := grammarOpts.Prefix
	maybeArray := grammarOpts.MaybeArray
	disableParallelNewLines := grammarOpts.DisableParallelNewLines
	maybeString := grammarOpts.MaybeString
	noMixedFreeString := grammarOpts.NoMixedFreeString

	var lines []string

	swapRoot := maybeArray || maybeString || prefix != ""

	// write down the computed rules.
	// if maybeArray is true, we need to add the array rule and slightly tweak the root rule
	for name, rule := range sc.rules {
		if swapRoot && name == "root" {
			name = "realvalue"
		}
		lines = append(lines, fmt.Sprintf("%s ::= %s", name, rule))
	}

	if !swapRoot {
		return strings.Join(lines, "\n")
	}

	newRoot := "realvalue"
	if maybeArray {
		newRoot = "arr | realvalue"
	}

	freestringRule := "mixedstring"
	if noMixedFreeString {
		freestringRule = "freestring"
	}

	if prefix != "" {
		// quote newlines in suffix
		prefix = utils.EscapeNewLines(prefix)

		if maybeArray && maybeString {
			newRoot = "(" + newRoot + ")"
		}

		if maybeString {
			//newRoot = "( (\"" + suffix + "\" " + newRoot + ") | freestring ) "
			newRoot = "( \"" + prefix + "\" " + newRoot + " | " + freestringRule + " ) "
		} else {
			newRoot = "\"" + prefix + "\" " + "" + newRoot + ""
		}
	} else if maybeString {
		if maybeArray {
			//	newRoot = "(" + newRoot + ")"
		}

		newRoot = freestringRule + " | " + newRoot
	}

	lines = append(lines, fmt.Sprintf("%s ::= %s", "root", newRoot))
	if disableParallelNewLines {
		lines = append(lines, array)
	} else {
		lines = append(lines, arrayNewLines)
	}

	if maybeArray {
		if grammarOpts.ExpectStringsAfterJSON {
			lines = append(lines, `mixedstring ::= freestring | freestring arr freestring | (freestring realvalue freestring)* | realvalue | arr`)
		} else {
			lines = append(lines, `mixedstring ::= freestring | freestring arr | freestring realvalue | realvalue | arr`)
		}
	} else {
		if grammarOpts.ExpectStringsAfterJSON {
			lines = append(lines, `mixedstring ::= freestring | (freestring realvalue freestring)* | realvalue`)
		} else {
			lines = append(lines, `mixedstring ::= freestring | freestring realvalue | realvalue`)
		}
	}

	return strings.Join(lines, "\n")
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

		fmt.Println("constant is ", constVal)
		literal, err := sc.formatLiteral((constVal))
		if err != nil {
			return "", err
		}
		fmt.Printf("literal is %s\n", literal)
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
		fmt.Printf("reading property %+v\n", properties)
		baseProperty := false
		depth := strings.Split(name, "-")
		if len(depth) == 2 {
			fmt.Printf("reading a base property %s\n", name)
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

		fmt.Printf("propPairs %+v\n", propPairs)

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
				fmt.Printf("visiting %s\n", fmt.Sprintf("%s-%s", ruleName, propName))
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
				fmt.Printf("visiting %s\n", fmt.Sprintf("%s-%s", ruleName, propName))
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
		fmt.Println("Property buildup")

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
	return sc.finalizeGrammar(options...), nil
}

func (sc *LLama31SchemaConverter) GrammarFromBytes(b []byte, options ...func(*GrammarOption)) (string, error) {
	var schema map[string]interface{}
	err := json.Unmarshal(b, &schema)
	if err != nil {
		return "", err
	}
	return sc.Grammar(schema, options...)
}
