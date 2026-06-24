//go:build routing_strict

package contract

import "fmt"

func panicIfStrict(name string, fields ...any) {
	panic(fmt.Sprintf("routing invariant violated under -tags=routing_strict: %s %v", name, fields))
}
