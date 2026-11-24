package nodes

// These export the load operation conformance to the external test package, so
// that the route table of package carrier (which imports this package) runs the
// same specs.

// LoadOperationHarness is loadOperationHarness.
type LoadOperationHarness = loadOperationHarness

// SentRequest is sentRequest.
type SentRequest = sentRequest

// ConformanceNode is the node of the conformance specs.
const ConformanceNode = conformanceNode

// RegisterLoadOperationCarrier adds a carrier to the conformance specs. Call it
// at package initialisation, before the specs are built.
func RegisterLoadOperationCarrier(name string, h func() LoadOperationHarness) {
	loadOperationCarriers[name] = h
}

// NewNATSLoadOperationHarness and NewHTTPLoadOperationHarness are the harnesses
// of the NATS and the tunnel carriers.
var (
	NewNATSLoadOperationHarness = newNATSLoadOperationHarness
	NewHTTPLoadOperationHarness = newHTTPLoadOperationHarness
)
