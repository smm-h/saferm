package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ParentEnv is the one variable ancestry travels through. It carries exactly
// one thing: the identifier of the entry describing the process that spawned
// this one.
const ParentEnv = "STRICTCLI_TRACE_PARENT"

// The anomaly kinds a capture can record. Every one of them describes
// something the consumer SAW, never a decision it took: an anomaly that
// vanishes is indistinguishable from a chain that was fine.
const (
	// AnomalyMalformedParentValue: the environment variable was set to
	// something that is not a canonical identifier. Recorded verbatim.
	AnomalyMalformedParentValue = "malformed-trace-parent"

	// AnomalyDanglingParent: an identifier resolved to no entry -- the store
	// was pruned or missing, the writer was another tool, or someone set the
	// variable by hand. Legal by design, and the store's primary
	// failure-detection channel.
	AnomalyDanglingParent = "dangling-parent"

	// AnomalyMalformedEntry: a line in a partition could not be read as an
	// entry -- torn by a non-atomic write, missing one of the thirteen keys, or
	// carrying an unparseable identifier.
	AnomalyMalformedEntry = "malformed-entry"

	// AnomalyStoreUnreadable: a partition or the store directory could not be
	// read at all. A store that does not exist is NOT this: that is an ordinary
	// dangling parent.
	AnomalyStoreUnreadable = "store-unreadable"

	// AnomalyChainCycle: walking parent_id revisited an identifier. No store a
	// conforming writer produces can contain one, since an entry's parent is
	// always older than itself.
	AnomalyChainCycle = "chain-cycle"
)

// maxAnomalyValueBytes caps what a single anomaly copies out of the store. A
// torn line has no length limit -- a partition can reach 8 MB -- and the whole
// capture is written into a database row, so the value is the head of what was
// seen rather than all of it.
const maxAnomalyValueBytes = 1024

// partitionRe matches a partition filename. Readers ignore every other file in
// the store directory; the write-failure marker is such a file.
var partitionRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2})\.jsonl$`)

// Entry is one line of the store: an invocation that spawned a child. Every key
// is always present in a conforming line, so an absent one makes the line
// malformed rather than defaulted.
type Entry struct {
	ID                   string  `json:"id"`
	ParentID             *string `json:"parent_id"`
	App                  string  `json:"app"`
	Version              string  `json:"version"`
	Command              *string `json:"command"`
	DryRun               bool    `json:"dry_run"`
	MachineMode          bool    `json:"machine_mode"`
	Quiet                bool    `json:"quiet"`
	Verbose              bool    `json:"verbose"`
	ApproveConsequential bool    `json:"approve_consequential"`
	Effect               string  `json:"effect"`
	PID                  int     `json:"pid"`
	SpawnedAt            string  `json:"spawned_at"`
}

// entryKeys is the set every conforming line carries.
var entryKeys = []string{
	"id", "parent_id", "app", "version", "command", "dry_run", "machine_mode",
	"quiet", "verbose", "approve_consequential", "effect", "pid", "spawned_at",
}

// Anomaly is something the capture saw and could not treat as well-formed.
type Anomaly struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Value  string `json:"value,omitempty"`
}

// Capture is what one deletion records about its ancestry.
//
// Chain holds the flattened ancestry, nearest caller first, so the record stays
// readable after the store is pruned. ChainIDs is the same walk as bare
// identifiers, kept for correlation with whatever store data still exists.
type Capture struct {
	// ParentID is the environment variable's value verbatim, well-formed or
	// not.
	ParentID  string    `json:"parent_id"`
	ChainIDs  []string  `json:"chain_ids,omitempty"`
	Chain     []Entry   `json:"chain,omitempty"`
	Anomalies []Anomaly `json:"anomalies,omitempty"`
}

// Collect resolves the ancestry of the running process from the store.
//
// It returns nil when STRICTCLI_TRACE_PARENT is unset -- nothing claimed this
// invocation, which is not an anomaly and is the state every deletion is in
// until callers upgrade to a framework that writes the store.
func Collect() *Capture {
	value := os.Getenv(ParentEnv)
	if value == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		c := &Capture{ParentID: value}
		c.record(AnomalyStoreUnreadable, "the home directory holding the trace store could not be resolved: "+err.Error(), "")
		return c
	}
	return collectFrom(StoreDir(home), value)
}

// StoreDir is the store's literal path under home.
//
// It is deliberately NOT derived from XDG_DATA_HOME or any other variable,
// despite matching the XDG default: a writer that honoured XDG_DATA_HOME and
// one that did not would write to two stores on the same machine, and a chain
// crossing them would dangle at both ends while both writers behaved correctly.
func StoreDir(home string) string {
	return filepath.Join(home, ".local", "share", "strictcli", "trace")
}

// collectFrom is Collect against a named store and a given variable value, so
// the resolution can be tested against fixture stores.
func collectFrom(store, value string) *Capture {
	if value == "" {
		return nil
	}
	c := &Capture{ParentID: value}
	if _, ok := ulidTimestamp(value); !ok {
		c.record(AnomalyMalformedParentValue,
			ParentEnv+" is not a canonical identifier under the strict ULID profile", value)
		return c
	}

	r := &reader{store: store}
	seen := make(map[string]bool)
	id := value
	for {
		if seen[id] {
			c.record(AnomalyChainCycle,
				"walking parent_id revisited an entry; the chain was cut here", id)
			return c
		}
		seen[id] = true

		entry, ok := r.lookup(id, c)
		if !ok {
			c.record(AnomalyDanglingParent,
				"no entry in the trace store carries this identifier", id)
			return c
		}
		c.Chain = append(c.Chain, *entry)
		c.ChainIDs = append(c.ChainIDs, entry.ID)

		if entry.ParentID == nil {
			return c // the root
		}
		id = *entry.ParentID
		if _, ok := ulidTimestamp(id); !ok {
			c.record(AnomalyMalformedEntry,
				"an entry's parent_id is not a canonical identifier, so the chain could not be walked further", id)
			return c
		}
	}
}

// Origin is the immediate caller's declared name and version -- the two values
// a deletion records as its origin. Both are nil when nothing resolved, which
// is what "no tool claimed this" means.
func (c *Capture) Origin() (name, version *string) {
	if c == nil || len(c.Chain) == 0 {
		return nil, nil
	}
	entry := c.Chain[0]
	if entry.App == "" {
		return nil, nil
	}
	name = &entry.App
	if entry.Version != "" {
		version = &entry.Version
	}
	return name, version
}

func (c *Capture) record(kind, detail, value string) {
	if len(value) > maxAnomalyValueBytes {
		value = value[:maxAnomalyValueBytes]
		detail += fmt.Sprintf(" (value truncated to the first %d bytes)", maxAnomalyValueBytes)
	}
	c.Anomalies = append(c.Anomalies, Anomaly{Kind: kind, Detail: detail, Value: value})
}

// reader resolves identifiers against a store, parsing each partition it needs
// at most once.
type reader struct {
	store  string
	labels []string // sorted partition labels, without the .jsonl suffix
	listed bool
	files  map[string]map[string]*Entry
}

// lookup finds the entry with the given identifier, recording anything
// malformed it meets on the way.
//
// The search starts deterministically: a partition's filename is its range
// start, a writer clamps an entry whose clock reads earlier than that start, and
// so no entry lands in a file whose range begins after it. The candidate file is
// therefore the greatest label not after the identifier's timestamp -- one
// binary search over the sorted filenames.
//
// The candidate can miss, because the clamp invariant is one-sided: it forbids
// an entry EARLIER than its file's range start and says nothing about one that
// is later. A writer that has already selected the active partition and then
// appends after another writer rolled to a new one strands an entry whose
// timestamp belongs to the newer file inside the older one. Treating the miss as
// a dangling parent would report an entry that is right there as missing, so on
// a miss the reader walks BACKWARD through the older partitions until the entry
// is found or the labels run out.
//
// Cost: the common case is unchanged -- one binary search and one file read. A
// miss costs at most one read per older partition, each parsed at most once for
// the whole capture (the reader caches by label), so a whole chain walk is
// bounded by the number of partition files in the store, not by the chain
// length. A genuinely absent identifier -- a pruned or foreign one -- is the
// worst case and reads every partition once.
func (r *reader) lookup(id string, c *Capture) (*Entry, bool) {
	ms, ok := ulidTimestamp(id)
	if !ok {
		return nil, false
	}
	r.list(c)
	if len(r.labels) == 0 {
		return nil, false
	}

	want := label(ms)
	// The first label strictly after want, minus one, is the greatest label not
	// after it.
	i := sort.SearchStrings(r.labels, want)
	if i < len(r.labels) && r.labels[i] == want {
		i++
	}
	if i == 0 {
		return nil, false // every partition begins after this identifier
	}
	for j := i - 1; j >= 0; j-- {
		if entry, ok := r.read(r.labels[j], c)[id]; ok {
			return entry, true
		}
	}
	return nil, false
}

// list reads the store directory once. A store that does not exist is not an
// error: it means tracing resumed from empty, or was pruned, and every
// identifier then dangles -- which is the same case a consumer of a foreign
// identifier is already in.
func (r *reader) list(c *Capture) {
	if r.listed {
		return
	}
	r.listed = true
	r.files = make(map[string]map[string]*Entry)

	names, err := os.ReadDir(r.store)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.record(AnomalyStoreUnreadable, "the trace store could not be read: "+err.Error(), r.store)
		}
		return
	}
	for _, name := range names {
		if m := partitionRe.FindStringSubmatch(name.Name()); m != nil {
			r.labels = append(r.labels, m[1])
		}
	}
	sort.Strings(r.labels)
}

// read parses one partition into an identifier index, once.
func (r *reader) read(label string, c *Capture) map[string]*Entry {
	if entries, ok := r.files[label]; ok {
		return entries
	}
	entries := make(map[string]*Entry)
	r.files[label] = entries

	path := filepath.Join(r.store, label+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		c.record(AnomalyStoreUnreadable, "a trace partition could not be read: "+err.Error(), path)
		return entries
	}
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		entry, err := parseEntry(raw)
		if err != nil {
			c.record(AnomalyMalformedEntry, "a line in "+label+".jsonl is not a conforming entry: "+err.Error(), raw)
			continue
		}
		entries[entry.ID] = entry
	}
	return entries
}

// parseEntry reads one line under the entry rules: every key present, the
// identifier canonical, and every value of the declared type.
func parseEntry(raw string) (*Entry, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, err
	}
	for _, key := range entryKeys {
		if _, ok := keys[key]; !ok {
			return nil, errors.New("missing key " + key)
		}
	}
	var entry Entry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return nil, err
	}
	if _, ok := ulidTimestamp(entry.ID); !ok {
		return nil, errors.New("id is not a canonical identifier under the strict ULID profile")
	}
	return &entry, nil
}

// label is the UTC-hour label for an instant: the partition's range start. All
// of it is UTC epoch arithmetic -- no local time, no timezone database, no
// calendar code.
func label(ms int64) string {
	return time.Unix((ms/3600000)*3600, 0).UTC().Format("2006-01-02T15")
}
