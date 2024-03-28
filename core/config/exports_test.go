package config

// This file re-exports private functions to be used directly in unit tests.
// Since this file's name ends in _test.go, theoretically these should not be exposed past the tests.

var ReadBackendConfigFile = readBackendConfigFile
