//go:build unix

package trace

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A store file is whatever is on disk under a partition's name, and a name is
// all a reader has to go on. A FIFO named like a partition opens and then blocks
// until someone writes to it, which would hang the deletion -- the read is not
// interruptible and no timeout wraps it. So the reader stats first and skips
// anything that is not a regular file, recording that it saw one.
func TestCollect_FIFONamedLikeAPartitionIsSkipped(t *testing.T) {
	leaf := encodeULID(msHour07, "0000000000000003")
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(leaf, "", "safegit", "0.25.0"),
	})
	// Newer than the entry's own hour, so the search lands on it first and the
	// backward walk still has to get past it.
	fifo := filepath.Join(store, "2026-08-13T09.jsonl")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("mkfifo is unavailable here: %v", err)
	}

	type result struct {
		capture *Capture
	}
	done := make(chan result, 1)
	go func() {
		done <- result{capture: collectFrom(store, encodeULID(msHour09, "0000000000000009"))}
	}()

	var c *Capture
	select {
	case r := <-done:
		c = r.capture
	case <-time.After(15 * time.Second):
		// The goroutine is stuck in the open(2) of the FIFO and will stay stuck;
		// the test binary exits and takes it with it.
		t.Fatal("reading a store directory holding a FIFO blocked; a delete would never finish")
	}

	var skipped bool
	for _, a := range c.Anomalies {
		if a.Kind == AnomalyStoreUnreadable && a.Value == fifo {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("the FIFO was not recorded as an unreadable store file: %+v", c.Anomalies)
	}

	// And the partitions that ARE regular files still resolve: one bad file in
	// the directory does not cost the store.
	done = make(chan result, 1)
	go func() { done <- result{capture: collectFrom(store, leaf)} }()
	select {
	case r := <-done:
		if len(r.capture.Chain) != 1 {
			t.Errorf("the readable partition did not resolve: %+v", r.capture)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("resolving an entry in a readable partition blocked")
	}
}
