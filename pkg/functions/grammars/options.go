package grammars

type GrammarOption struct {
	PropOrder               string
	Prefix                  string
	MaybeArray              bool
	DisableParallelNewLines bool
	MaybeString             bool
	NoMixedFreeString       bool
	ExpectStringsAfterJSON  bool

	FunctionName string
	SchemaType   SchemaConverterType
}

func (o *GrammarOption) Apply(options ...func(*GrammarOption)) {
	for _, l := range options {
		l(o)
	}
}

var EnableMaybeArray = func(o *GrammarOption) {
	o.MaybeArray = true
}

var DisableParallelNewLines = func(o *GrammarOption) {
	o.DisableParallelNewLines = true
}

var EnableMaybeString = func(o *GrammarOption) {
	o.MaybeString = true
}

var NoMixedFreeString func(*GrammarOption) = func(o *GrammarOption) {
	o.NoMixedFreeString = true
}

// ExpectStringsAfterJSON enables mixed string suffix
var ExpectStringsAfterJSON func(*GrammarOption) = func(o *GrammarOption) {
	o.ExpectStringsAfterJSON = true
}

func SetPrefix(suffix string) func(*GrammarOption) {
	return func(o *GrammarOption) {
		o.Prefix = suffix
	}
}

func SetPropOrder(order string) func(*GrammarOption) {
	return func(o *GrammarOption) {
		o.PropOrder = order
	}
}

func WithSchemaType(schemaType SchemaConverterType) func(*GrammarOption) {
	return func(o *GrammarOption) {
		o.SchemaType = schemaType
	}
}

func WithFunctionName(name string) func(*GrammarOption) {
	return func(o *GrammarOption) {
		o.FunctionName = name
	}
}
