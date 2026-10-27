package config

// ResetLegacyKeyWarnings forgets which old keys were named, for the tests.
func ResetLegacyKeyWarnings() {
	legacyKeyMu.Lock()
	defer legacyKeyMu.Unlock()
	legacyKeyWarned = map[string]bool{}
}
