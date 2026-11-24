// Package builtin registers the engines LocalAI ships with enginesettings.
// To add an engine, add its package to Registry.
package builtin

import (
	"sync"

	"github.com/mudler/LocalAI/pkg/enginesettings"
	"github.com/mudler/LocalAI/pkg/enginesettings/llamacpp"
)

// Registry returns the registry of the built-in engines. It is built once and
// only read after that.
var Registry = sync.OnceValue(func() *enginesettings.Registry {
	r := enginesettings.NewRegistry()
	for _, e := range []*enginesettings.Engine{
		llamacpp.Engine(),
	} {
		if err := r.Register(e); err != nil {
			panic(err)
		}
	}
	return r
})
