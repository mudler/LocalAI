package config

// ModelConfigRevisionTransition describes one authoritative model identity.
// Related transitions are applied together by revision lifecycle services.
type ModelConfigRevisionTransition struct {
	ModelName      string
	ConfigRevision string
	Disabled       bool
}
