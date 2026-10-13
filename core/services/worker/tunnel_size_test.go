//go:build !race

package worker

// transferBytes is the size of the transfer in the specs that measure the delay
// of a small call. The race detector slows the copy so much that a build with it
// moves less (see tunnel_size_race_test.go).
const transferBytes = 256 << 20
