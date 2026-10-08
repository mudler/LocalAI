//go:build !windows

package worker

import "syscall"

// unreachableErrnos are the errors of a dial that say the target did not
// answer. See classifyServiceFailure.
var unreachableErrnos = []error{
	syscall.ECONNREFUSED,
	syscall.EHOSTUNREACH,
	syscall.ENETUNREACH,
	syscall.ECONNRESET,
	syscall.ECONNABORTED,
}
