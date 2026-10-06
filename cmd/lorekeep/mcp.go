package main

import (
	"fmt"
	"io"
)

// mcpCmd runs lorekeep mcp and lorekeep mcp install.
func mcpCmd(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "lorekeep mcp: not implemented")
	return exitUsage
}
