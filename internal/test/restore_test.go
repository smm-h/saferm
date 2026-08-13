package test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/smm-h/saferm/internal/testutil"
)

// archivedUUID reads the uuid out of one `delete` line: the command prints
// `archived: [<id>] <uuid> <path> (<size>)` for every record it writes, and the
// uuid is what names the entry in the archive directory.
var archivedUUID = regexp.MustCompile(`archived: \[\d+\] ([0-9a-f-]{36}) `)

func parseArchivedUUID(t *testing.T, stdout string) string {
	t.Helper()
	m := archivedUUID.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("no archived line with a uuid in: %q", stdout)
	}
	return m[1]
}

// writeCorruptTarZst replaces path with a .tar.zst holding one complete member
// followed by garbage, so an extraction writes that member and then fails. It
// is the shape a truncated or bit-rotted directory archive takes on the way
// back.
func writeCorruptTarZst(t *testing.T, path string, top string) {
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

// archiveEntry returns the path of a record's archive entry inside the test's
// own saferm home.
func archiveEntry(homeDir string, uuid string, suffix string) string {
	return filepath.Join(homeDir, ".saferm", "archive", uuid+suffix)
}

// A directory restore that fails partway must leave three things true: the
// archived copy is still in the archive, the destination does not look
// restored, and the record is still restorable. Nothing about the restore is
// verified up front here -- the destination is absent, so nothing is destroyed
// by trying -- which is exactly why the failure has to be harmless.
func TestUndelete_CorruptDirectoryArchive_KeepsTheCopyAndLeavesNoHalfTree(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	tree := testutil.CreateTempDir(t, workDir, "tree")
	testutil.CreateTempFile(t, tree, "first.txt", "first member\n")
	testutil.CreateTempFile(t, tree, "second.txt", "second member\n")

	stdout, stderr, code := runSaferm(t, homeDir, "delete", "--on-error", "abort", "-r", "--description", "corrupt archive test", tree)
	if code != 0 {
		t.Fatalf("delete failed (exit %d): stderr=%q", code, stderr)
	}
	uuid := parseArchivedUUID(t, stdout)
	entry := archiveEntry(homeDir, uuid, ".tar.zst")
	writeCorruptTarZst(t, entry, "tree")

	_, stderr, code = runSaferm(t, homeDir, "undelete", uuid)
	if code == 0 {
		t.Fatalf("restoring a corrupt directory archive must fail; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "had been extracted") {
		t.Errorf("the failure must report what it managed to extract, got: %q", stderr)
	}
	if !strings.Contains(stderr, "still restorable") {
		t.Errorf("the failure must say the record can be tried again, got: %q", stderr)
	}

	if _, err := os.Stat(entry); err != nil {
		t.Errorf("a failed restore must keep the archived copy: %v", err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("a failed restore must leave no half tree at the destination, got: %v", err)
	}

	// The record was never stamped as restored, so it is still listed as
	// restorable and a retry is possible.
	infoOut, _, code := runSaferm(t, homeDir, "info", uuid)
	if code != 0 {
		t.Fatalf("info failed after the failed restore: %q", infoOut)
	}
	if !strings.Contains(infoOut, "restorable") {
		t.Errorf("the record must still be restorable, got: %q", infoOut)
	}
}

// The preview and the real restore walk one step list, so a directory restore's
// would-do log names the same three acts the real one performs: the parent
// directory, the tree appearing at the destination, and the archived copy being
// dropped afterwards.
func TestUndelete_DirectoryDryRunRecordsTheWholeRestore(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	tree := testutil.CreateTempDir(t, workDir, "tree")
	testutil.CreateTempFile(t, tree, "inner.txt", "content\n")

	stdout, stderr, code := runSaferm(t, homeDir, "delete", "--on-error", "abort", "-r", "--description", "preview seed", tree)
	if code != 0 {
		t.Fatalf("delete failed (exit %d): stderr=%q", code, stderr)
	}
	uuid := parseArchivedUUID(t, stdout)
	entry := archiveEntry(homeDir, uuid, ".tar.zst")

	stdout, stderr, code = runSaferm(t, homeDir, "--dry-run", "undelete", uuid)
	if code != 0 {
		t.Fatalf("undelete --dry-run failed (exit %d): stderr=%q", code, stderr)
	}
	log := wouldDoLog(stdout)
	if !strings.Contains(log, "write: "+tree) {
		t.Errorf("the preview must name the tree it would restore, got: %q", log)
	}
	if !strings.Contains(log, "remove: "+entry) {
		t.Errorf("the preview must name the archived copy it would consume, got: %q", log)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("a dry-run restore recreated %s for real", tree)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Errorf("a dry-run restore consumed the archived copy: %v", err)
	}
}

// The symlink shape goes through the same list: the recorded target is written
// out as the preview's content, and the .symlink entry is consumed last.
func TestUndelete_SymlinkDryRunRecordsTheWholeRestore(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	target := testutil.CreateTempFile(t, workDir, "target.txt", "target\n")
	link := filepath.Join(workDir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runSaferm(t, homeDir, "delete", "--on-error", "abort", "--description", "symlink preview seed", link)
	if code != 0 {
		t.Fatalf("delete failed (exit %d): stderr=%q", code, stderr)
	}
	uuid := parseArchivedUUID(t, stdout)
	entry := archiveEntry(homeDir, uuid, ".symlink")

	stdout, stderr, code = runSaferm(t, homeDir, "--dry-run", "undelete", uuid)
	if code != 0 {
		t.Fatalf("undelete --dry-run failed (exit %d): stderr=%q", code, stderr)
	}
	log := wouldDoLog(stdout)
	if !strings.Contains(log, "write: "+link) {
		t.Errorf("the preview must name the link it would recreate, got: %q", log)
	}
	if !strings.Contains(log, "remove: "+entry) {
		t.Errorf("the preview must name the archived entry it would consume, got: %q", log)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("a dry-run restore recreated %s for real", link)
	}

	// And the real one performs exactly that.
	if _, stderr, code = runSaferm(t, homeDir, "undelete", uuid); code != 0 {
		t.Fatalf("undelete failed (exit %d): stderr=%q", code, stderr)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the link was not recreated: %v", err)
	}
	if got != target {
		t.Errorf("symlink target mismatch: got %q, want %q", got, target)
	}
	if _, err := os.Lstat(entry); !os.IsNotExist(err) {
		t.Errorf("a completed restore must consume the archived entry, got: %v", err)
	}
}
