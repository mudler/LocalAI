package functions

import "encoding/json"

type Item struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type JSONFunctionStructure struct {
	OneOf []Item                 `json:"oneOf,omitempty"`
	AnyOf []Item                 `json:"anyOf,omitempty"`
	Defs  map[string]interface{} `json:"$defs,omitempty"`
}

func (j JSONFunctionStructure) Grammar(options ...func(*GrammarOption)) (string, error) {
	grammarOpts := &GrammarOption{}
	grammarOpts.Apply(options...)

	dat, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return NewJSONSchemaConverter(grammarOpts.PropOrder).GrammarFromBytes(dat, options...)
}
