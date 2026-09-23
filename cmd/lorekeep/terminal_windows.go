package main

import (
	"os"
	"syscall"
)

// isTerminal reports whether f is a console a person can type into. A file
// mode is not enough on Windows: the NUL device, which CI and scripts give
// as stdin, is a character device too, and a prompt read from it would take
// its default answer on nobody's behalf.
func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
