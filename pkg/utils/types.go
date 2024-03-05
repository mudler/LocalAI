package utils

type ErrorOr[T any] struct {
	Value T
	Error error
}
