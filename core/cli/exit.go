package cli

import "fmt"

// ExitCodeError is a failure a command has already reported to the user. It
// carries nothing but the status the process should exit with, and main prints
// nothing more for it.
//
// It exists for commands that hand their terminal to something that does its
// own error reporting. Returning that subordinate's error instead would put a
// bare "exit status 1" underneath the explanation the user has just read, and
// returning nil would tell a script the run succeeded.
type ExitCodeError struct{ Code int }

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }
