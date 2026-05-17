package test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smm-h/saferm/internal/testutil"
)

func TestConcurrentDeletes(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()

	const numFiles = 20
	const maxRetries = 3

	// Create all temp files.
	files := make([]string, numFiles)
	for i := range numFiles {
		name := fmt.Sprintf("concurrent_%03d.txt", i)
		content := fmt.Sprintf("content of file %d", i)
		files[i] = testutil.CreateTempFile(t, workDir, name, content)
	}

	// Launch parallel deletes with retry on SQLITE_BUSY.
	var wg sync.WaitGroup
	results := make([]struct {
		stdout   string
		stderr   string
		exitCode int
	}, numFiles)

	wg.Add(numFiles)
	for i := range numFiles {
		go func(idx int) {
			defer wg.Done()
			desc := fmt.Sprintf("concurrent %d", idx)
			var stdout, stderr string
			var code int
			for attempt := range maxRetries {
				stdout, stderr, code = runSaferm(t, homeDir, "delete",
					"--description", desc, files[idx])
				if code == 0 {
					break
				}
				// Retry on database lock errors.
				if strings.Contains(stderr, "SQLITE_BUSY") || strings.Contains(stderr, "database is locked") {
					time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
					continue
				}
				break
			}
			results[idx].stdout = stdout
			results[idx].stderr = stderr
			results[idx].exitCode = code
		}(i)
	}
	wg.Wait()

	// Verify all deletes succeeded.
	for i, r := range results {
		if r.exitCode != 0 {
			t.Errorf("delete %d failed (exit %d): stderr=%q", i, r.exitCode, r.stderr)
		}
	}

	// Verify list shows all items.
	stdout, stderr, code := runSaferm(t, homeDir, "list")
	if code != 0 {
		t.Fatalf("list failed (exit %d): stderr=%q", code, stderr)
	}

	ids := parseAllIDs(t, stdout)
	if len(ids) != numFiles {
		t.Errorf("expected %d items in list, got %d:\n%s", numFiles, len(ids), stdout)
	}

	// Verify all records are individually queryable via info.
	for _, id := range ids {
		_, stderr, code := runSaferm(t, homeDir, "info", id)
		if code != 0 {
			t.Errorf("info %s failed (exit %d): stderr=%q", id, code, stderr)
		}
	}

	// Verify list output contains all concurrent file names.
	for i := range numFiles {
		name := fmt.Sprintf("concurrent_%03d.txt", i)
		if !strings.Contains(stdout, name) {
			t.Errorf("list output missing %s", name)
		}
	}
}
