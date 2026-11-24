package modeladmin

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelAdmin(t *testing.T) {
	RegisterFailHandler(Fail)
	// Several specs in this suite coordinate goroutines through
	// Eventually/Consistently on unbuffered-ish channels (e.g. the
	// blockingRevisionLifecycle helper). Gomega's 1s default timeout can be
	// too tight on slower or loaded CI runners (notably macOS runners),
	// causing spurious "Timed out after 1.005s" failures even though the
	// goroutines eventually make progress. Give them more headroom.
	SetDefaultEventuallyTimeout(5 * time.Second)
	RunSpecs(t, "modeladmin test suite")
}
