package grammars

type SchemaConverterType int

const (
	JSONSchema SchemaConverterType = iota
	LLama31Schema
)

const (
	LlamaType string = "llama3.1"
	JSONType  string = "json"
)

func (s SchemaConverterType) String() string {
	switch s {
	case JSONSchema:
		return JSONType
	case LLama31Schema:
		return LlamaType
	}
	return "unknown"
}

func NewType(t string) SchemaConverterType {
	switch t {
	case JSONType:
		return JSONSchema
	case LlamaType:
		return LLama31Schema
	}
	return JSONSchema
}
