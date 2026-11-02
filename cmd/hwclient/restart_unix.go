//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// replaceProcess turns this process into a fresh run of executable. It keeps
// the PID and the terminal, so a headless run under a shell or a service
// manager carries on as the same process.
func replaceProcess(executable string, args []string) error {
	argv := append([]string{executable}, args...)
	if err := syscall.Exec(executable, argv, os.Environ()); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	return nil // Not reached: Exec only returns on failure.
}
