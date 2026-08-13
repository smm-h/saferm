package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The copy-and-verify fallback and the classification that reaches it.
//
// A hard link is refused for four reasons and no test process can produce any
// of them on a directory it owns: EXDEV needs a second filesystem, EPERM needs
// protected_hardlinks and a file belonging to somebody else, EOPNOTSUPP/ENOSYS
// need a filesystem without links, EMLINK needs 65000 links. The seam in
// archive.go stands in for the kernel so the fallback is exercised somewhere
// other than production, and linkRefused's classification -- which decides
// whether saferm copies or reports -- is checked directly against the errnos.

func TestLinkRefused_Classification(t *testing.T) {
	refusals := []syscall.Errno{
		syscall.EXDEV,      // a different filesystem
		syscall.EPERM,      // protected_hardlinks, or a filesystem that rejects links
		syscall.EOPNOTSUPP, // no hard links here
		syscall.ENOSYS,     // no hard links at all
		syscall.EMLINK,     // the inode is at its link limit
	}
	for _, errno := range refusals {
		err := &os.LinkError{Op: "link", Old: "a", New: "b", Err: errno}
		if !linkRefused(err) {
			t.Errorf("%v (%d) must be classified as a refusal, so the copy fallback runs", errno, int(errno))
		}
	}

	// Genuine failures. Copying would fail the same way and hide the reason, so
	// each of these is reported as itself.
	failures := []syscall.Errno{
		syscall.ENOENT, // the archive directory is not there
		syscall.ENOSPC, // the disk is full
		syscall.EROFS,  // the archive is on a read-only mount
		syscall.EACCES, // the archive directory is not writable
		syscall.EDQUOT, // over quota
	}
	for _, errno := range failures {
		err := &os.LinkError{Op: "link", Old: "a", New: "b", Err: errno}
		if linkRefused(err) {
			t.Errorf("%v (%d) is a real failure and must not be mistaken for a refusal", errno, int(errno))
		}
	}

	if linkRefused(errors.New("not a link error at all")) {
		t.Error("an error that is not an *os.LinkError cannot be a link refusal")
	}
	if linkRefused(&os.LinkError{Op: "link", Err: errors.New("not an errno")}) {
		t.Error("an *os.LinkError carrying no errno cannot be classified as a refusal")
	}
	if linkRefused(nil) {
		t.Error("linkRefused(nil) must be false")
	}
}

func TestCopyAndVerify_CopiesAndAcceptsAMatchingHash(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	content := []byte("the bytes that were hashed")
	if err := os.WriteFile(src, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)

	if err := copyAndVerify(src, dst, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("copyAndVerify: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("the copy is not there: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("the copy holds %q", got)
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(srcInfo, dstInfo) {
		t.Error("copyAndVerify produced a link, not a copy")
	}
	if dstInfo.Mode().Perm() != srcInfo.Mode().Perm() {
		t.Errorf("the copy's permissions are %v, the original's are %v", dstInfo.Mode().Perm(), srcInfo.Mode().Perm())
	}
}

// A copy whose hash does not match the one already recorded is not an archive
// entry, and leaving it would put content in the archive under a name whose
// record describes something else. It is removed and the mismatch is reported.
func TestCopyAndVerify_MismatchRemovesTheCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("some content"), 0600); err != nil {
		t.Fatal(err)
	}

	err := copyAndVerify(src, dst, strings.Repeat("0", 64))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("the unverifiable copy was left in the archive (err=%v)", err)
	}
}

func TestCopyAndVerify_MissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	if err := copyAndVerify(filepath.Join(dir, "nope.txt"), dst, strings.Repeat("0", 64)); err == nil {
		t.Fatal("copying a file that does not exist must fail")
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("a failed copy left something behind (err=%v)", err)
	}
}

// End to end through Execute: a refused link produces the same archived entry
// by copying, with the hash the record will carry, and the plan remembers that
// the entry is NOT the source's inode -- which is what tells RemoveSource that
// a later write through the path did not reach the archived bytes.
func TestExecute_CopiesWhenTheLinkIsRefused(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EXDEV, syscall.EPERM, syscall.EOPNOTSUPP, syscall.ENOSYS, syscall.EMLINK} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			refuseLinks(t, errno)

			content := []byte("archived by copying")
			src := filepath.Join(dir, "src.txt")
			if err := os.WriteFile(src, content, 0644); err != nil {
				t.Fatal(err)
			}

			plan, err := NewPlan(src, filepath.Join(dir, "archive"), false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Execute(plan)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if plan.linked {
				t.Error("the plan claims a link although the link was refused")
			}

			sum := sha256.Sum256(content)
			if result.Hash != hex.EncodeToString(sum[:]) {
				t.Errorf("hash %s does not describe the archived content", result.Hash)
			}
			if got, err := os.ReadFile(plan.Dest); err != nil || string(got) != string(content) {
				t.Fatalf("the copied entry holds %q (err=%v)", got, err)
			}
			if err := RemoveSource(plan); err != nil {
				t.Fatalf("RemoveSource after a copied archival: %v", err)
			}
			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Errorf("the source survived (err=%v)", err)
			}
		})
	}
}

// A link failure that is not a refusal is reported as itself. Copying would
// meet the same wall -- a missing archive directory, a full disk -- and
// reporting a hash mismatch or a copy error instead would name the wrong cause.
func TestExecute_ReportsALinkFailureThatIsNotARefusal(t *testing.T) {
	dir := t.TempDir()
	refuseLinks(t, syscall.ENOSPC)

	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(src, filepath.Join(dir, "archive"), false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Execute(plan)
	if err == nil {
		t.Fatal("a link failure that is not a refusal must fail the archival")
	}
	if !strings.Contains(err.Error(), "linking file into archive") {
		t.Errorf("the error must name the link, got: %v", err)
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Errorf("the error must carry the errno it failed with, got: %v", err)
	}
	if _, err := os.Lstat(plan.Dest); !os.IsNotExist(err) {
		t.Errorf("a failed archival left an entry behind (err=%v)", err)
	}
	if _, err := os.Lstat(src); err != nil {
		t.Errorf("a failed archival must leave the source alone: %v", err)
	}
}
