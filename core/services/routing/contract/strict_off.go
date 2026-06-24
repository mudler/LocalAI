//go:build !routing_strict

package contract

func panicIfStrict(name string, fields ...any) {}
