package archive

import (
	"os"
	"syscall"
	"testing"
)

// refuseLinks makes every os.Link call in this package fail with errno for the
// duration of t.
func refuseLinks(t *testing.T, errno syscall.Errno) {
	t.Helper()
	original := linkFile
	linkFile = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errno}
	}
	t.Cleanup(func() { linkFile = original })
}
