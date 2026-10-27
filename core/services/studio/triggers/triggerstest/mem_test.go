package triggerstest

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

func TestMemStore(t *testing.T) {
	RunStoreSuite(t, func(*testing.T) triggers.Store { return NewMemStore() })
}
