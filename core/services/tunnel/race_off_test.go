//go:build !race

package tunnel_test

// transferSize is the size of the transfer in the latency spec. The race
// detector slows the copy so much that the transfer would take most of a
// minute, so a build with it moves less (see race_on_test.go).
const transferSize = 256 << 20
