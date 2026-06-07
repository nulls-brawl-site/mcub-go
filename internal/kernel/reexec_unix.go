//go:build !windows

package kernel

import (
	"syscall"
)

// reExec replaces the current process with a fresh execution of exe.
// On UNIX this uses syscall.Exec (execve), which is a true in-place replacement.
func reExec(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), syscall.Environ())
}
