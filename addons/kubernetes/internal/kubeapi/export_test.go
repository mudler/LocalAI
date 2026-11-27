// SPDX-License-Identifier: MIT
package kubeapi

// CheckDial is checkDial, for the tests.
var CheckDial = checkDial

// NewLoopbackFactoryForTest is the factory with loopback allowed, so a test
// server on the loopback interface can answer.
var NewLoopbackFactoryForTest = func() Factory { return newFactory(true) }
