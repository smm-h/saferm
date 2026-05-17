//go:build darwin

package meta

import (
	"fmt"
	"os/exec"
	"strings"
)

// readParentCmdline retrieves the command line of the given PID via ps.
func readParentCmdline(ppid int) string {
	cmd := exec.Command("ps", "-o", "command=", "-p", fmt.Sprintf("%d", ppid))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
