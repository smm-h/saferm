//go:build linux

package meta

import (
	"fmt"
	"os"
	"strings"
)

// readParentCmdline reads the command line of the given PID from /proc.
func readParentCmdline(ppid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", ppid))
	if err != nil {
		return ""
	}
	// /proc/<pid>/cmdline uses null bytes as separators.
	s := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.TrimSpace(s)
}
