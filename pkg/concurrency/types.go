package concurrency

type ErrorOr[T any] struct {
	Value T
	Error error
}
