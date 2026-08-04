package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// These tests pin the strictcli effects regime as saferm exposes it: the
// confirm protocol in front of every mutating command, and a --dry-run that
// records what it would destroy and destroys nothing.

// dryRunLogHeader is the first line strictcli writes to stdout at the end of
// every dry-run dispatch. It is never suppressed.
const dryRunLogHeader = "DRY RUN — no changes were made. Would do:"

// wouldDoLog returns the would-do log portion of stdout, or "" when the run was
// not a dry run.
func wouldDoLog(stdout string) string {
	if i := strings.Index(stdout, dryRunLogHeader); i >= 0 {
		return stdout[i:]
	}
	return ""
}

// TestDeleteRefusesWithoutConsent: `delete` is `mutating`, so a run nobody
// approved is stopped before dispatch and the file stays where it is.
func TestDeleteRefusesWithoutConsent(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "keep.txt", "content\n")

	_, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "unconsented", target)
	if code == 0 {
		t.Fatalf("an unconsented delete must not succeed; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "--yes") && !strings.Contains(stderr, "aborted") {
		t.Errorf("the refusal must show that consent was missing, got: %s", stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("an unconsented delete removed %s anyway: %v", target, err)
	}
}

// TestListNeedsNoConsent: `list` is `read_only` and never prompts.
func TestListNeedsNoConsent(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	if _, stderr, code := runSafermNoConsent(t, home, "list"); code != 0 {
		t.Fatalf("a read-only command must run without consent, got %d: %s", code, stderr)
	}
}

// TestDeleteDryRunRecordsAndDeletesNothing is the deliberate probe: saferm's
// whole purpose is deletion with an audit trail, so a preview must name exactly
// what it would move and leave the file on disk.
func TestDeleteDryRunRecordsAndDeletesNothing(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "doomed.txt", "content\n")

	stdout, stderr, code := runSaferm(t, home, "--dry-run", "delete", "--description", "preview", target)
	if code != 0 {
		t.Fatalf("delete --dry-run failed (%d): %s", code, stderr)
	}

	log := wouldDoLog(stdout)
	if log == "" {
		t.Fatalf("dry mode must render the would-do log, got: %q", stdout)
	}
	if !strings.Contains(log, "rename: "+target) {
		t.Errorf("the would-do log must name the file it would move, got: %s", log)
	}
	if !strings.Contains(log, "mkdir:") {
		t.Errorf("the would-do log must record the archive-directory creation, got: %s", log)
	}
	if !strings.Contains(stdout, "would be archived") {
		t.Errorf("a preview must not claim files were archived, got: %s", stdout)
	}

	if _, err := os.Stat(target); err != nil {
		t.Errorf("a dry-run delete removed %s: %v", target, err)
	}

	// Nothing may have been recorded in the archive either.
	listOut, _, _ := runSaferm(t, home, "list")
	if strings.Contains(listOut, "doomed.txt") {
		t.Errorf("a dry-run delete wrote a database record: %s", listOut)
	}
}

// TestDeleteDirectoryDryRunRecordsAndDeletesNothing: the directory shape is a
// tar+zstd write plus a recursive removal, and both must appear.
func TestDeleteDirectoryDryRunRecordsAndDeletesNothing(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	dir := testutil.CreateTempDir(t, work, "tree")
	testutil.CreateTempFile(t, dir, "inner.txt", "content\n")

	stdout, stderr, code := runSaferm(t, home, "--dry-run", "delete", "-r", "--description", "preview", dir)
	if code != 0 {
		t.Fatalf("delete -r --dry-run failed (%d): %s", code, stderr)
	}
	log := wouldDoLog(stdout)
	if !strings.Contains(log, "write:") {
		t.Errorf("the would-do log must record the archive write, got: %s", log)
	}
	if !strings.Contains(log, "remove: "+dir) {
		t.Errorf("the would-do log must record the tree removal, got: %s", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "inner.txt")); err != nil {
		t.Errorf("a dry-run recursive delete removed the tree: %v", err)
	}
}

// TestPurgeDryRunRecordsAndDestroysNothing: purge is the irreversible half of
// saferm, so its preview must name every archive file it would destroy and
// carry the grant that says why.
func TestPurgeDryRunRecordsAndDestroysNothing(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "gone.txt", "content\n")

	if _, stderr, code := runSaferm(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding the archive failed: %s", stderr)
	}

	stdout, stderr, code := runSaferm(t, home, "--dry-run", "purge", "--all", "--skip-confirmation")
	if code != 0 {
		t.Fatalf("purge --dry-run failed (%d): %s", code, stderr)
	}
	log := wouldDoLog(stdout)
	if !strings.Contains(log, "remove:") {
		t.Errorf("the would-do log must record the archive-file removal, got: %s", log)
	}
	if !strings.Contains(log, "granted: purge") {
		t.Errorf("the recorded removal must carry its grant, got: %s", log)
	}

	// The item is still listed and still restorable.
	listOut, _, _ := runSaferm(t, home, "list")
	if !strings.Contains(listOut, "gone.txt") {
		t.Errorf("a dry-run purge removed the record: %s", listOut)
	}
	if _, stderr, code := runSaferm(t, home, "undelete", target); code != 0 {
		t.Fatalf("a dry-run purge destroyed the content; undelete failed: %s", stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("undelete after a dry-run purge did not restore the file: %v", err)
	}
}

// TestUndeleteDryRunRestoresNothing.
func TestUndeleteDryRunRestoresNothing(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "back.txt", "content\n")

	if _, stderr, code := runSaferm(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding the archive failed: %s", stderr)
	}

	stdout, stderr, code := runSaferm(t, home, "--dry-run", "undelete", target)
	if code != 0 {
		t.Fatalf("undelete --dry-run failed (%d): %s", code, stderr)
	}
	if !strings.Contains(wouldDoLog(stdout), "rename:") {
		t.Errorf("the would-do log must record the restore, got: %s", stdout)
	}
	if _, err := os.Stat(target); err == nil {
		t.Errorf("a dry-run undelete restored %s for real", target)
	}
}
