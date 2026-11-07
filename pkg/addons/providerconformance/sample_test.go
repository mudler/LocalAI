package providerconformance_test

import (
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/providerconformance"
	"github.com/mudler/LocalAI/pkg/addons/providerconformance/sample"
)

// The sample external provider passes the suite every provider must pass.
func TestSampleProviderConforms(t *testing.T) {
	providerconformance.Run(t, providerconformance.Subject{Manifest: sample.Manifest, Call: sample.New().Call})
}
