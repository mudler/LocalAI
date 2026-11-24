package backend

import (
	"sync"

	"github.com/mudler/localrecall/rag"
)

// CollectionList maps collection names to their persistent knowledge bases.
type CollectionList map[string]*rag.PersistentKB

// State holds in-memory state for the collections API.
type State struct {
	backend          *backendInProcess
	Mu               sync.RWMutex
	Collections      CollectionList
	SourceManager    *rag.SourceManager
	EnsureCollection func(name string) (*rag.PersistentKB, bool) // get-or-create; the shared collections use it
}
