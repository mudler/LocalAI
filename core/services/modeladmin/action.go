package modeladmin

// Action is the verb passed to ToggleState / TogglePinned. The typed alias
// catches typos at compile time (a stray "enabled" or "Pin" never reaches
// the runtime check) and lets callers reference the canonical strings via
// the constants below rather than re-typing them.
type Action string

const (
	ActionEnable  Action = "enable"
	ActionDisable Action = "disable"
	ActionPin     Action = "pin"
	ActionUnpin   Action = "unpin"
)

// Valid reports whether a is one of the allowed actions for a given
// operation. ToggleState passes ActionEnable/ActionDisable; TogglePinned
// passes ActionPin/ActionUnpin.
func (a Action) Valid(allowed ...Action) bool {
	for _, x := range allowed {
		if a == x {
			return true
		}
	}
	return false
}
