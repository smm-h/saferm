package main

import (
	"errors"
	"strings"
	"testing"
)

// A tree extraction that fails partway takes its own half tree back and says
// what it had written. The branch below is the other half of that sentence:
// the rollback itself can fail, and then some of those paths are still
// standing at the destination.
//
// It is assembled directly rather than provoked end to end because provoking
// it means making os.RemoveAll fail on paths saferm has just created, which a
// test cannot arrange inside a temporary directory it owns. What the branch
// owes the caller is the naming: told only "the restore failed", a caller
// would expect an untouched destination and go looking for one that is not
// there.
func TestPartialExtraction_NamesWhatCouldNotBeRemovedAgain(t *testing.T) {
	inner := errors.New("unexpected EOF")
	err := &partialExtraction{
		err:       inner,
		extracted: []string{"/dest/tree", "/dest/tree/a.txt", "/dest/tree/b.txt"},
		stuck:     []string{"/dest/tree/b.txt"},
	}

	msg := err.Error()

	// Why the extraction stopped, and how far it had got.
	if !strings.Contains(msg, "unexpected EOF") {
		t.Errorf("the message must carry why the extraction failed, got: %q", msg)
	}
	if !strings.Contains(msg, "3 path(s) had been extracted") {
		t.Errorf("the message must count what had been extracted, got: %q", msg)
	}
	if !strings.Contains(msg, "/dest/tree/a.txt") {
		t.Errorf("the message must name what had been extracted, got: %q", msg)
	}

	// And the part that is still there, by name -- the reason this branch
	// exists at all.
	if !strings.Contains(msg, "1 of them could not be removed again") {
		t.Errorf("the message must count what the rollback could not take back, got: %q", msg)
	}
	if !strings.Contains(msg, "(/dest/tree/b.txt)") {
		t.Errorf("the message must name the path that is still standing, got: %q", msg)
	}
	// A rollback that failed must never claim the clean outcome's ending.
	if strings.Contains(msg, "and were removed again") {
		t.Errorf("a stuck path means the half tree was NOT all removed again, got: %q", msg)
	}

	if !errors.Is(err, inner) {
		t.Errorf("the wrapped failure must stay reachable, got: %v", errors.Unwrap(err))
	}
}

// The same failure with nothing stuck says the opposite ending: everything it
// wrote was taken back, so the destination is as it was.
func TestPartialExtraction_SaysSoWhenTheRollbackTookEverythingBack(t *testing.T) {
	err := &partialExtraction{
		err:       errors.New("unexpected EOF"),
		extracted: []string{"/dest/tree", "/dest/tree/a.txt"},
	}

	msg := err.Error()
	if !strings.Contains(msg, "and were removed again") {
		t.Errorf("a clean rollback must say the destination was left as it was, got: %q", msg)
	}
	if strings.Contains(msg, "could not be removed again") {
		t.Errorf("nothing was stuck, so nothing is claimed to be, got: %q", msg)
	}
}
