package vz

// Skips says which part of the conformance suite this backend cannot do. A
// suspend does not keep the processes of the guest (see Suspend). Nothing else
// is skipped.
func (b *Backend) Skips(feature string) bool { return feature == "suspend-processes" }
