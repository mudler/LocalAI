// SPDX-License-Identifier: MIT

//go:build kubernetes_e2e

package kubeapi

// NewLoopbackFactory is NewFactory with loopback addresses allowed, for the
// end-to-end suite, whose API server listens on the loopback interface. It
// is built only with the kubernetes_e2e build tag, so the add-on binary has
// no way to allow loopback addresses.
func NewLoopbackFactory() Factory { return newFactory(true) }
