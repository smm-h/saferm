package main

import (
	"fmt"
	"os"
)

const (
	ExitSuccess      = 0
	ExitGeneral      = 1
	ExitUsage        = 2
	ExitFileNotFound = 3
	ExitPermission   = 4
	ExitDatabase     = 5
	ExitArchive      = 6
	ExitConflict     = 7
)

func die(code int, format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
