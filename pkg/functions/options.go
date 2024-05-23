package functions

type GrammarOption struct {
	PropOrder               string
	Suffix                  string
	MaybeArray              bool
	DisableParallelNewLines bool
	MaybeString             bool
	NoMixedFreeString       bool
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

func SetPrefix(suffix string) func(*GrammarOption) {
	return func(o *GrammarOption) {
		o.Suffix = suffix
	}
}
