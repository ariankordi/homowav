//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// replaceProcess starts a new copy of executable and exits this one. Windows
// has no exec(), so the new copy gets a new PID.
func replaceProcess(executable string, args []string) error {
	command := exec.Command(executable, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	os.Exit(0)
	return nil
}
