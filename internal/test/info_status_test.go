package test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// A record and its archived content have separate fates: a restore consumes
// the blob, a purge destroys it, and in both cases the record survives as
// metadata with nothing left to restore. `info` used to print the two
// timestamps and leave the reader to work out what they implied. The status
// line states it, and is derived from the columns that already record it --
// there is no separate detection pass over the archive directory.

var statusLine = regexp.MustCompile(`(?m)^Status:\s+(.*)$`)

func infoStatus(t *testing.T, home, target string) string {
	t.Helper()
	stdout, stderr, code := runSaferm(t, home, "info", target)
	if code != 0 {
		t.Fatalf("info %s failed (%d): %s", target, code, stderr)
	}
	m := statusLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("info %s printed no status line:\n%s", target, stdout)
	}
	return strings.TrimSpace(m[1])
}

func TestInfo_StatusIsRestorableThenRestored(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "status.txt", "content\n")

	_, uuid := deleteOne(t, home, target)

	if got := infoStatus(t, home, uuid); got != "restorable" {
		t.Errorf("a freshly archived record must be restorable, got %q", got)
	}

	if _, stderr, code := runSaferm(t, home, "undelete", uuid); code != 0 {
		t.Fatalf("undelete failed (%d): %s", code, stderr)
	}

	got := infoStatus(t, home, uuid)
	if !strings.HasPrefix(got, "restored at ") {
		t.Errorf("a consumed record must report when it was restored, got %q", got)
	}
}

func TestInfo_StatusReportsPurged(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "purged-status.txt", "content\n")

	_, uuid := deleteOne(t, home, target)

	if _, stderr, code := runSaferm(t, home, "--approve-consequential", "purge", uuid); code != 0 {
		t.Fatalf("purge failed (%d): %s", code, stderr)
	}

	got := infoStatus(t, home, uuid)
	if !strings.HasPrefix(got, "purged at ") {
		t.Errorf("a purged record must report when it was purged, got %q", got)
	}
}

// Restored and then purged is a real state: the restore consumed the blob and
// the purge stamped the record afterwards. Both facts are reported, in the
// order they happened.
func TestInfo_StatusReportsRestoredAndPurged(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	target := testutil.CreateTempFile(t, work, "both.txt", "content\n")

	_, uuid := deleteOne(t, home, target)

	if _, stderr, code := runSaferm(t, home, "undelete", uuid); code != 0 {
		t.Fatalf("undelete failed (%d): %s", code, stderr)
	}
	if _, stderr, code := runSaferm(t, home, "--approve-consequential", "purge", uuid); code != 0 {
		t.Fatalf("purge failed (%d): %s", code, stderr)
	}

	got := infoStatus(t, home, uuid)
	if !strings.Contains(got, "restored at ") || !strings.Contains(got, "purged at ") {
		t.Errorf("a restored-then-purged record must report both, got %q", got)
	}
}
