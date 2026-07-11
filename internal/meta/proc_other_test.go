//go:build !linux && !darwin

package meta

import "testing"

// TestReadParentCmdline_Unavailable verifies that on platforms without a
// portable parent-cmdline mechanism, readParentCmdline honestly reports
// "unavailable" as an empty string rather than fabricating a value.
func TestReadParentCmdline_Unavailable(t *testing.T) {
	if got := readParentCmdline(1); got != "" {
		t.Errorf("readParentCmdline on unsupported platform should return \"\", got %q", got)
	}
}
