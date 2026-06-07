//go:build windows

package kernel

import (
	"os"
	"os/exec"
)

// reExec restarts the process on Windows by spawning a new child and exiting.
func reExec(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
