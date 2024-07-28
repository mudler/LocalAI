package functions

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/functions/grammars"
)

type Item struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type JSONFunctionStructure struct {
	OneOf []Item                 `json:"oneOf,omitempty"`
	AnyOf []Item                 `json:"anyOf,omitempty"`
	Defs  map[string]interface{} `json:"$defs,omitempty"`
}

func (j JSONFunctionStructure) Grammar(options ...func(*grammars.GrammarOption)) (string, error) {
	grammarOpts := &grammars.GrammarOption{}
	grammarOpts.Apply(options...)

	dat, err := json.Marshal(j)
	if err != nil {
		return "", err
	}

	converter := NewSchemaConverter(*grammarOpts)
	return converter.GrammarFromBytes(dat, options...)
}

type SchemaConverter interface {
	GrammarFromBytes([]byte, ...func(*grammars.GrammarOption)) (string, error)
}

func NewSchemaConverter(opt grammars.GrammarOption) SchemaConverter {
	switch {
	case opt.SchemaType == grammars.LLama31Schema:
		return grammars.NewLLama31SchemaConverter(opt.FunctionName)
	}
	return grammars.NewJSONSchemaConverter(opt.PropOrder)
}
