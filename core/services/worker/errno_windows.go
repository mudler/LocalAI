//go:build windows

package worker

import "syscall"

// unreachableErrnos are the errors of a dial that say the target did not
// answer. See classifyServiceFailure. Windows reports the WSA values and not
// the invented values of package syscall, so both are listed.
var unreachableErrnos = []error{
	syscall.ECONNREFUSED,
	syscall.EHOSTUNREACH,
	syscall.ENETUNREACH,
	syscall.ECONNRESET,
	syscall.ECONNABORTED,
	syscall.Errno(10061), // WSAECONNREFUSED
	syscall.Errno(10065), // WSAEHOSTUNREACH
	syscall.Errno(10051), // WSAENETUNREACH
	syscall.Errno(10054), // WSAECONNRESET
	syscall.Errno(10053), // WSAECONNABORTED
}
