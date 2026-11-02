package main

import (
	"fmt"
	"os"
	"slices"
)

// relaunchArgs is the command line for the new copy: the same flags as this
// one, plus any of extra that are not already there.
func relaunchArgs(current, extra []string) []string {
	args := slices.Clone(current)
	for _, flag := range extra {
		if !slices.Contains(args, flag) {
			args = append(args, flag)
		}
	}
	return args
}

// relaunch starts this program again with the same flags plus extra, and
// does not return on success. The caller must have shut the server down
// first, so the new copy can bind the port.
func relaunch(extra []string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: cannot find this program: %w", err)
	}
	return replaceProcess(executable, relaunchArgs(os.Args[1:], extra))
}
