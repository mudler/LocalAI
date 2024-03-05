package utils

type ErrorOr[T any] struct {
	Value T
	Error error
}

// ??
type SourceValue[S any, V any] struct {
	Source S
	Value  V
}
