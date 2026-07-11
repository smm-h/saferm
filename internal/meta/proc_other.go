//go:build !linux && !darwin

package meta

// readParentCmdline is not implemented on this platform. Parent-cmdline
// capture is best-effort metadata; where no portable mechanism exists
// (e.g. Windows), it honestly reports "unavailable" as an empty string,
// which meta.go stores as an omitted ParentCmd field.
func readParentCmdline(ppid int) string {
	return ""
}
