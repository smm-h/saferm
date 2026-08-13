package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// restoreNow performs the archive-side half of a restore the way the command
// does it: the parent directory, then the kind's own act, then the consumption
// of the entry -- always in that order, so a failure keeps the archived copy.
//
// The command owns the decision this helper does not make: what to do about a
// destination that already exists. That is not an archive-layer question any
// more, because answering it needs the record and the caller's stated conflict
// mode.
func restoreNow(uuid string, archiveDir string, dest string, isDirectory bool, symlinkTarget string) error {
	p := NewRestorePlan(uuid, archiveDir, dest, isDirectory, symlinkTarget)
	if err := EntryPresent(p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	switch p.Kind {
	case KindSymlink:
		if err := RestoreSymlink(p); err != nil {
			return err
		}
	case KindDirectory:
		if _, err := ExtractTree(p); err != nil {
			return err
		}
	default:
		if err := os.Rename(p.Entry, p.Dest); err != nil {
			if !IsCrossDeviceError(err) {
				return err
			}
			return CopyOut(p.Entry, p.Dest)
		}
		return nil
	}
	return os.Remove(p.Entry)
}

// corruptTarZst writes a .tar.zst at path whose stream holds one complete
// member and then ends in garbage, so an extraction writes that member and
// fails on the next header. It is the shape a truncated or bit-rotted archive
// takes on the way back, and the only way to reach the partial-extraction path
// deliberately.
func corruptTarZst(t *testing.T, path string, top string) {
	t.Helper()

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: top, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	body := []byte("first member\n")
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: top + "/first.txt", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	// Flush pads the member to its block boundary but writes no terminator, so
	// what follows is read as the next header.
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	raw.Write(bytes.Repeat([]byte{0xff}, 512))

	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// A directory restore that fails partway through must leave the archived copy
// exactly where it is: the entry is consumed by the caller only after the
// extraction succeeds, so a retry has something to read. The extraction also
// names every path it created, which is what lets the caller undo the half tree
// it left at the destination.
func TestExtractTree_FailurePartway_KeepsEntryAndNamesWhatItWrote(t *testing.T) {
	tmpDir := t.TempDir()
	archiveDir := filepath.Join(tmpDir, "archive")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	uuid := NewUUID()
	entry := filepath.Join(archiveDir, uuid+".tar.zst")
	corruptTarZst(t, entry, "tree")

	dest := filepath.Join(tmpDir, "tree")
	p := NewRestorePlan(uuid, archiveDir, dest, true, "")

	created, err := ExtractTree(p)
	if err == nil {
		t.Fatal("extracting a corrupt archive must fail")
	}
	if _, statErr := os.Stat(entry); statErr != nil {
		t.Fatalf("a failed extraction must keep the archived copy: %v", statErr)
	}
	if len(created) == 0 {
		t.Fatal("the extraction must name the paths it created before it failed")
	}
	first := filepath.Join(dest, "first.txt")
	var sawFirst bool
	for _, c := range created {
		if c == first {
			sawFirst = true
		}
	}
	if !sawFirst {
		t.Errorf("the member written before the failure must be named, got: %v", created)
	}
	if _, statErr := os.Stat(first); statErr != nil {
		t.Fatalf("the member written before the failure should be on disk: %v", statErr)
	}
}

// The half tree is undone, newest path first, so the destination is left as it
// was found rather than looking restored when it is not.
func TestRollbackExtraction_RemovesWhatTheExtractionCreated(t *testing.T) {
	tmpDir := t.TempDir()
	archiveDir := filepath.Join(tmpDir, "archive")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	uuid := NewUUID()
	entry := filepath.Join(archiveDir, uuid+".tar.zst")
	corruptTarZst(t, entry, "tree")

	dest := filepath.Join(tmpDir, "tree")
	p := NewRestorePlan(uuid, archiveDir, dest, true, "")

	created, err := ExtractTree(p)
	if err == nil {
		t.Fatal("extracting a corrupt archive must fail")
	}
	if stuck := RollbackExtraction(created); len(stuck) != 0 {
		t.Fatalf("the rollback should have removed everything, stuck: %v", stuck)
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Errorf("the rollback must remove the destination it created, got: %v", statErr)
	}
	if _, statErr := os.Stat(entry); statErr != nil {
		t.Errorf("the rollback must not touch the archived copy: %v", statErr)
	}
}

// A destination directory the extraction did NOT create is left standing: the
// empty-destination rule restores a tree into its own emptied original place,
// and undoing the extraction must not remove the directory the caller owns.
func TestRollbackExtraction_LeavesAPreExistingDestination(t *testing.T) {
	tmpDir := t.TempDir()
	archiveDir := filepath.Join(tmpDir, "archive")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	uuid := NewUUID()
	entry := filepath.Join(archiveDir, uuid+".tar.zst")
	corruptTarZst(t, entry, "tree")

	dest := filepath.Join(tmpDir, "tree")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewRestorePlan(uuid, archiveDir, dest, true, "")

	created, _ := ExtractTree(p)
	if stuck := RollbackExtraction(created); len(stuck) != 0 {
		t.Fatalf("the rollback should have removed everything it created, stuck: %v", stuck)
	}
	info, err := os.Lstat(dest)
	if err != nil || !info.IsDir() {
		t.Fatalf("a pre-existing destination must survive the rollback: %v", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the destination must be empty again after the rollback, got: %v", entries)
	}
}

// CopyOut is the cross-device half of a file restore: it copies the archived
// copy to the destination and consumes it only once the copy is there.
func TestCopyOut_ConsumesTheEntryOnlyAfterTheCopy(t *testing.T) {
	tmpDir := t.TempDir()
	entry := filepath.Join(tmpDir, "entry")
	dest := filepath.Join(tmpDir, "dest.txt")
	content := []byte("archived bytes\n")
	if err := os.WriteFile(entry, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CopyOut(entry, dest); err != nil {
		t.Fatalf("CopyOut failed: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("restored content mismatch: got %q, want %q", got, content)
	}
	if _, err := os.Lstat(entry); !os.IsNotExist(err) {
		t.Errorf("a completed restore must consume the archived copy, got: %v", err)
	}
}

// A copy that fails keeps the archived copy and takes its own partial
// destination back, so nothing is left looking restored.
func TestCopyOut_FailureKeepsTheEntry(t *testing.T) {
	tmpDir := t.TempDir()
	entry := filepath.Join(tmpDir, "entry")
	if err := os.WriteFile(entry, []byte("archived bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A destination inside a directory nothing can write to: the copy fails at
	// the open, before a byte is written.
	blocked := filepath.Join(tmpDir, "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o700) })
	dest := filepath.Join(blocked, "dest.txt")

	if err := CopyOut(entry, dest); err == nil {
		t.Fatal("a copy into an unwritable directory must fail")
	}
	if _, err := os.Lstat(entry); err != nil {
		t.Errorf("a failed restore must keep the archived copy: %v", err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Errorf("a failed copy must leave no partial destination, got: %v", err)
	}
}

// The plan resolves the three entry shapes by reading only, and names the file
// the restore will consume.
func TestNewRestorePlan_ResolvesTheThreeShapes(t *testing.T) {
	uuid := NewUUID()
	archiveDir := "/archive"

	file := NewRestorePlan(uuid, archiveDir, "/dest", false, "")
	if file.Kind != KindFile || file.Entry != filepath.Join(archiveDir, uuid) {
		t.Errorf("file plan wrong: kind=%v entry=%s", file.Kind, file.Entry)
	}
	dir := NewRestorePlan(uuid, archiveDir, "/dest", true, "")
	if dir.Kind != KindDirectory || dir.Entry != filepath.Join(archiveDir, uuid+".tar.zst") {
		t.Errorf("directory plan wrong: kind=%v entry=%s", dir.Kind, dir.Entry)
	}
	link := NewRestorePlan(uuid, archiveDir, "/dest", false, "../elsewhere")
	if link.Kind != KindSymlink || link.Entry != filepath.Join(archiveDir, uuid+".symlink") {
		t.Errorf("symlink plan wrong: kind=%v entry=%s", link.Kind, link.Entry)
	}
	if link.SymlinkTarget != "../elsewhere" {
		t.Errorf("symlink target lost: %q", link.SymlinkTarget)
	}
}

// A missing entry is reported as the archive-level absence it is, before
// anything at the destination is touched.
func TestEntryPresent_NamesAMissingEntry(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewRestorePlan(NewUUID(), tmpDir, filepath.Join(tmpDir, "dest"), false, "")
	err := EntryPresent(p)
	if err == nil {
		t.Fatal("a missing entry must be reported")
	}
	if !strings.Contains(err.Error(), ErrEntryMissing.Error()) {
		t.Errorf("expected ErrEntryMissing, got: %v", err)
	}
}
