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

func (j JSONFunctionStructure) Grammar(options ...func(*GrammarOption)) string {
	grammarOpts := &GrammarOption{}
	grammarOpts.Apply(options...)

	dat, _ := json.Marshal(j)
	return NewJSONSchemaConverter(grammarOpts.PropOrder).GrammarFromBytes(dat, options...)
}
