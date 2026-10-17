//go:build !unix

package cms

import "os/exec"

// stopGroup leaves exec's default in place where there are no process
// groups to stop: a timeout kills the command itself, and WaitDelay still
// bounds how long its output is read for.
func stopGroup(*exec.Cmd) {}

// killGroup has nothing to do without process groups.
func killGroup(*exec.Cmd) {}
