package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// These tests pin the strictcli effects regime as saferm exposes it: the
// confirm protocol in front of the one consequential command, and a --dry-run
// that records what it would destroy and destroys nothing.

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

// TestDeleteNeedsNoConsent: `delete` is `mutating` but NOT `consequential` --
// it moves a file into the archive, from which `undelete` brings it back. The
// confirm protocol keys on the declaration, not on the classification, so bare
// `saferm delete` must dispatch straight through with nothing added to argv, in
// a spawned process with no terminal to confirm at.
//
// This is the regression that matters most: while the protocol inferred the
// prompt from `mutating`, bare `saferm delete` refused, and every documented
// convention that spells it bare was broken.
func TestDeleteNeedsNoConsent(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "doomed.txt", "content\n")

	_, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "bare delete", target)
	if code != 0 {
		t.Fatalf("bare `saferm delete` must succeed with no approval flag; code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "Proceed?") || strings.Contains(stderr, "approve-consequential") {
		t.Errorf("a recoverable delete must not raise the confirm protocol, got: %s", stderr)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the bare delete did not archive %s (err=%v)", target, err)
	}
}

// TestUndeleteNeedsNoConsent: `undelete` restores a file to its original path
// and refuses to clobber an existing one without --force-overwrite, so it is
// recoverable by construction and never prompts either.
func TestUndeleteNeedsNoConsent(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "restore-me.txt", "content\n")

	if _, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding delete failed (%d): %s", code, stderr)
	}
	_, stderr, code := runSafermNoConsent(t, home, "undelete", "1")
	if code != 0 {
		t.Fatalf("bare `saferm undelete` must succeed with no approval flag; code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the bare undelete did not restore %s: %v", target, err)
	}
}

// TestPurgeRefusesWithoutConsent: `purge` is the one command that declares
// itself `consequential` -- it destroys archived content permanently, and
// nothing in saferm can bring it back. An unapproved run is stopped before
// dispatch and the archive is left intact.
func TestPurgeRefusesWithoutConsent(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "archived.txt", "content\n")

	if _, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding delete failed (%d): %s", code, stderr)
	}

	_, stderr, code := runSafermNoConsent(t, home, "purge", "--all", "--skip-confirmation")
	if code == 0 {
		t.Fatalf("an unapproved purge must not succeed; stderr=%s", stderr)
	}
	const want = "error: stdin is not interactive; pass --approve-consequential to confirm"
	if !strings.Contains(stderr, want) {
		t.Errorf("expected %q on stderr, got: %s", want, stderr)
	}

	// The item is still listed, so nothing was destroyed.
	stdout, _, code := runSafermNoConsent(t, home, "list")
	if code != 0 || !strings.Contains(stdout, "archived.txt") {
		t.Errorf("the unapproved purge destroyed the archived item; list: %s", stdout)
	}
}

// TestPurgeWithApprovalProceeds: --approve-consequential is the deliberate
// approval, and (paired with --skip-confirmation for saferm's own listing
// prompt) the purge runs.
func TestPurgeWithApprovalProceeds(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "archived.txt", "content\n")

	if _, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding delete failed (%d): %s", code, stderr)
	}

	_, stderr, code := runSafermNoConsent(t, home,
		"--approve-consequential", "purge", "--all", "--skip-confirmation")
	if code != 0 {
		t.Fatalf("an approved purge must run, got %d: %s", code, stderr)
	}
	stdout, _, _ := runSafermNoConsent(t, home, "list")
	if strings.Contains(stdout, "archived.txt") {
		t.Errorf("the approved purge did not destroy the archived item; list: %s", stdout)
	}
}

// TestDeclinedPurgePromptExitsNonzero pins the exit code of saferm's OWN
// listing prompt, which sits behind the framework gate. A declined purge is a
// refusal, not a success: exiting 0 told a script that the items were destroyed
// when nothing happened, and a non-interactive run reaches this branch by
// reading EOF.
func TestDeclinedPurgePromptExitsNonzero(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "archived.txt", "content\n")

	if _, stderr, code := runSafermNoConsent(t, home, "delete", "--description", "seed", target); code != 0 {
		t.Fatalf("seeding delete failed (%d): %s", code, stderr)
	}

	// Approved at the framework gate, but saferm's own prompt reads EOF.
	stdout, stderr, code := runSafermNoConsent(t, home, "--approve-consequential", "purge", "--all")
	if code == 0 {
		t.Errorf("a declined purge must exit nonzero, got 0; stdout=%s stderr=%s", stdout, stderr)
	}
	list, _, _ := runSafermNoConsent(t, home, "list")
	if !strings.Contains(list, "archived.txt") {
		t.Errorf("the declined purge destroyed the archived item anyway; list: %s", list)
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
