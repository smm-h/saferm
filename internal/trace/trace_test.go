package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodeULID is the writer's side of the identifier profile, present here only
// so tests can mint identifiers with a chosen millisecond. saferm never mints
// one: it is a consumer of the store, and a consumer only ever parses.
func encodeULID(ms int64, tail string) string {
	if len(tail) != 16 {
		panic("tail must be 16 characters")
	}
	head := make([]byte, 10)
	for i := 9; i >= 0; i-- {
		head[i] = crockford[ms&0x1F]
		ms >>= 5
	}
	return string(head) + tail
}

const (
	// 2026-08-13T04:17:52.913Z and 2026-08-13T07:30:00.000Z in epoch ms.
	msHour04 = 1786594672913
	msHour07 = 1786606200000
	// 2026-08-13T09:00:01.234Z: five hours past the 04 partition's range start.
	msHour09 = (msHour04/3600000)*3600000 + 5*3600000 + 1234
)

// writeStore lays out a store directory with the given partitions. Each value
// is the file's whole content, written verbatim so a test can put a torn or
// malformed line in it.
func writeStore(t *testing.T, partitions map[string]string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "trace")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatalf("creating store: %v", err)
	}
	for name, content := range partitions {
		if err := os.WriteFile(filepath.Join(store, name), []byte(content), 0600); err != nil {
			t.Fatalf("writing partition %s: %v", name, err)
		}
	}
	return store
}

// line renders one spec-conformant entry, with every one of the thirteen keys
// present.
func line(id string, parent string, app, version string) string {
	parentJSON := "null"
	if parent != "" {
		parentJSON = `"` + parent + `"`
	}
	return `{"id":"` + id + `","parent_id":` + parentJSON + `,"app":"` + app +
		`","version":"` + version + `","command":"release.run","dry_run":false,` +
		`"machine_mode":false,"quiet":false,"verbose":true,"approve_consequential":true,` +
		`"effect":"mutating","pid":48213,"spawned_at":"2026-08-13T04:17:52.913Z"}` + "\n"
}

func TestULIDProfile(t *testing.T) {
	valid := encodeULID(msHour04, "7QK2WVBD3F5RTYAC")
	ms, ok := ulidTimestamp(valid)
	if !ok {
		t.Fatalf("%q was rejected", valid)
	}
	if ms != msHour04 {
		t.Errorf("timestamp round-tripped as %d, want %d", ms, msHour04)
	}

	for _, bad := range []struct {
		label string
		text  string
	}{
		{"lowercase", strings.ToLower(valid)},
		{"too short", valid[:25]},
		{"too long", valid + "A"},
		{"I is not in the alphabet", "0" + "I" + valid[2:]},
		{"L is not in the alphabet", "0" + "L" + valid[2:]},
		{"O is not in the alphabet", "0" + "O" + valid[2:]},
		{"U is not in the alphabet", "0" + "U" + valid[2:]},
		{"overflows 128 bits", "8" + valid[1:]},
		{"empty", ""},
		{"non-ascii", "01JZ8X4M6N7QK2WVBD3F5RTYÅC"},
	} {
		if _, ok := ulidTimestamp(bad.text); ok {
			t.Errorf("%s: %q was accepted", bad.label, bad.text)
		}
	}
}

func TestCollect_UnsetVariableCapturesNothing(t *testing.T) {
	store := writeStore(t, nil)
	if c := collectFrom(store, ""); c != nil {
		t.Errorf("an unset variable produced a capture: %+v", c)
	}
}

func TestCollect_ResolvesChainAndOrigin(t *testing.T) {
	root := encodeULID(msHour04, "0000000000000001")
	middle := encodeULID(msHour04, "0000000000000002")
	// The leaf's identifier falls in hour 07, which has no partition of its
	// own: the 04 file's range runs until the 09 file's label, and the clamp
	// invariant is what makes that a deterministic lookup rather than a scan.
	leaf := encodeULID(msHour07, "0000000000000003")

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl":  line(root, "", "claudewheel", "0.20.0") + line(middle, root, "rlsbl", "0.61.2") + line(leaf, middle, "safegit", "0.25.0"),
		"2026-08-13T09.jsonl":  line(encodeULID(msHour07+7200000, "0000000000000004"), "", "unrelated", "1.2.3"),
		"write-failure.marker": "2026-08-13T04:17:52.913Z\n",
	})

	c := collectFrom(store, leaf)
	if c == nil {
		t.Fatal("no capture")
	}
	if len(c.Anomalies) != 0 {
		t.Errorf("a well-formed chain produced anomalies: %+v", c.Anomalies)
	}
	if c.ParentID != leaf {
		t.Errorf("ParentID is %q, want the environment variable's value %q", c.ParentID, leaf)
	}

	wantIDs := []string{leaf, middle, root}
	if len(c.ChainIDs) != len(wantIDs) {
		t.Fatalf("chain ids are %v, want %v", c.ChainIDs, wantIDs)
	}
	for i, want := range wantIDs {
		if c.ChainIDs[i] != want {
			t.Errorf("chain id %d is %q, want %q", i, c.ChainIDs[i], want)
		}
	}

	// The flattened chain is the record's own copy: pruning the store later
	// must not be able to orphan it.
	if len(c.Chain) != 3 {
		t.Fatalf("flattened chain holds %d entries, want 3", len(c.Chain))
	}
	if c.Chain[0].App != "safegit" || c.Chain[1].App != "rlsbl" || c.Chain[2].App != "claudewheel" {
		t.Errorf("chain apps are %q/%q/%q", c.Chain[0].App, c.Chain[1].App, c.Chain[2].App)
	}
	if c.Chain[2].ParentID != nil {
		t.Errorf("the root entry carries a parent: %v", *c.Chain[2].ParentID)
	}
	if c.Chain[0].Command == nil || *c.Chain[0].Command != "release.run" {
		t.Errorf("the leaf's command is %v", c.Chain[0].Command)
	}

	name, version := c.Origin()
	if name == nil || *name != "safegit" {
		t.Errorf("origin name is %v, want the immediate caller's app", name)
	}
	if version == nil || *version != "0.25.0" {
		t.Errorf("origin version is %v, want the immediate caller's version", version)
	}
}

// The clamp invariant is one-sided: it stops an entry from landing EARLIER than
// its file's range start, and says nothing about one landing later. A writer
// that selected the 04 file and then appended after another writer created the
// 09 file strands an entry whose timestamp is hour 09 inside the 04 file, so the
// binary search lands on 09, misses, and would report a dangling parent for an
// entry that is right there. The reader walks backward through older partitions
// on a miss.
func TestCollect_ResolvesAnEntryStrandedByARollover(t *testing.T) {
	stranded := encodeULID(msHour09, "0000000000000005")
	neighbour := encodeULID(msHour09+60000, "0000000000000006")

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(stranded, "", "safegit", "0.25.0"),
		"2026-08-13T09.jsonl": line(neighbour, "", "unrelated", "1.2.3"),
	})

	c := collectFrom(store, stranded)
	if c == nil {
		t.Fatal("no capture")
	}
	if len(c.Anomalies) != 0 {
		t.Errorf("a stranded but present entry produced anomalies: %+v", c.Anomalies)
	}
	if len(c.Chain) != 1 {
		t.Fatalf("the stranded entry did not resolve: %+v", c.Chain)
	}
	if name, version := c.Origin(); name == nil || *name != "safegit" || version == nil || *version != "0.25.0" {
		t.Errorf("origin is %v/%v, want the stranded entry's app and version", name, version)
	}
}

// A chain whose links are spread across partitions in either direction resolves
// whole: the leaf is stranded in an older file, its parent lives where the
// search would look for it, and its grandparent is older still.
func TestCollect_WalksBackwardAcrossSeveralPartitions(t *testing.T) {
	root := encodeULID(msHour04, "0000000000000001")
	middle := encodeULID(msHour07, "0000000000000002")
	leaf := encodeULID(msHour09, "0000000000000003")

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(root, "", "claudewheel", "0.20.0"),
		"2026-08-13T06.jsonl": line(middle, root, "rlsbl", "0.61.2") + line(leaf, middle, "safegit", "0.25.0"),
		"2026-08-13T09.jsonl": line(encodeULID(msHour09+60000, "0000000000000004"), "", "unrelated", "1.2.3"),
	})

	c := collectFrom(store, leaf)
	if len(c.Anomalies) != 0 {
		t.Errorf("a resolvable chain produced anomalies: %+v", c.Anomalies)
	}
	if len(c.Chain) != 3 {
		t.Fatalf("chain holds %d entries, want 3: %+v", len(c.Chain), c.ChainIDs)
	}
	if c.Chain[0].App != "safegit" || c.Chain[1].App != "rlsbl" || c.Chain[2].App != "claudewheel" {
		t.Errorf("chain apps are %q/%q/%q", c.Chain[0].App, c.Chain[1].App, c.Chain[2].App)
	}
}

// The walk is bounded by the partitions that exist: an identifier no file holds
// still dangles, once, after every older partition has been read.
func TestCollect_BackwardWalkTerminatesOnAnAbsentIdentifier(t *testing.T) {
	absent := encodeULID(msHour09, "000000000000DEAD")

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(encodeULID(msHour04, "0000000000000001"), "", "one", "1.0.0"),
		"2026-08-13T06.jsonl": line(encodeULID(msHour07, "0000000000000002"), "", "two", "2.0.0"),
		"2026-08-13T09.jsonl": line(encodeULID(msHour09+60000, "0000000000000003"), "", "three", "3.0.0"),
	})

	c := collectFrom(store, absent)
	if len(c.Chain) != 0 {
		t.Errorf("an absent identifier resolved a chain: %+v", c.Chain)
	}
	if len(c.Anomalies) != 1 || !hasAnomaly(c, AnomalyDanglingParent, absent) {
		t.Errorf("an absent identifier produced anomalies %+v", c.Anomalies)
	}
}

func TestCollect_MalformedVariableIsAnAnomaly(t *testing.T) {
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(encodeULID(msHour04, "0000000000000001"), "", "rlsbl", "0.61.2"),
	})

	for _, value := range []string{"not-a-ulid", strings.ToLower(encodeULID(msHour04, "0000000000000001")), "   "} {
		c := collectFrom(store, value)
		if c == nil {
			t.Fatalf("%q produced no capture; a polluted variable must not vanish", value)
		}
		if c.ParentID != value {
			t.Errorf("the variable was not recorded verbatim: %q", c.ParentID)
		}
		if len(c.Anomalies) != 1 || c.Anomalies[0].Kind != AnomalyMalformedParentValue {
			t.Fatalf("%q produced anomalies %+v", value, c.Anomalies)
		}
		if c.Anomalies[0].Value != value {
			t.Errorf("the anomaly does not hold the value verbatim: %q", c.Anomalies[0].Value)
		}
		if len(c.Chain) != 0 {
			t.Errorf("a malformed variable resolved a chain: %+v", c.Chain)
		}
		if name, version := c.Origin(); name != nil || version != nil {
			t.Errorf("a malformed variable filled an origin: %v %v", name, version)
		}
	}
}

func TestCollect_DanglingParentIsAnAnomaly(t *testing.T) {
	missing := encodeULID(msHour04, "000000000000DEAD")

	// The whole store is missing: pruned, deleted, or never written. Same case
	// as a foreign identifier -- legal by design, recorded, carried on from.
	empty := filepath.Join(t.TempDir(), "no-store")
	c := collectFrom(empty, missing)
	if c == nil {
		t.Fatal("no capture")
	}
	if len(c.Chain) != 0 {
		t.Errorf("a missing store resolved a chain: %+v", c.Chain)
	}
	if !hasAnomaly(c, AnomalyDanglingParent, missing) {
		t.Errorf("a missing store produced anomalies %+v", c.Anomalies)
	}
	if name, _ := c.Origin(); name != nil {
		t.Errorf("a dangling parent filled an origin: %v", *name)
	}

	// The leaf resolves, its parent does not: the chain stops there, the
	// origin is still the leaf's, and the break is recorded.
	leaf := encodeULID(msHour07, "0000000000000003")
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(leaf, missing, "safegit", "0.25.0"),
	})
	c = collectFrom(store, leaf)
	if len(c.Chain) != 1 {
		t.Fatalf("chain holds %d entries, want 1", len(c.Chain))
	}
	if !hasAnomaly(c, AnomalyDanglingParent, missing) {
		t.Errorf("the broken link produced anomalies %+v", c.Anomalies)
	}
	if name, _ := c.Origin(); name == nil || *name != "safegit" {
		t.Errorf("origin is %v; a dangling grandparent does not unclaim the deletion", name)
	}
}

func TestCollect_MalformedLinesAreAnomalies(t *testing.T) {
	leaf := encodeULID(msHour04, "0000000000000003")
	torn := `{"id":"01JZ8X4M6N7QK2WVBD3F5RT`
	// Every key is always present: an absent key is a malformed line, not a
	// defaulted one.
	missingKey := `{"id":"` + encodeULID(msHour04, "0000000000000009") + `","parent_id":null,"app":"x","version":"1"}`

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": torn + "\n" + missingKey + "\n" + line(leaf, "", "safegit", "0.25.0"),
	})

	c := collectFrom(store, leaf)
	if len(c.Chain) != 1 {
		t.Fatalf("the well-formed entry did not resolve: %+v", c.Chain)
	}
	kinds := map[string]int{}
	for _, a := range c.Anomalies {
		kinds[a.Kind]++
	}
	if kinds[AnomalyMalformedEntry] != 2 {
		t.Errorf("malformed-entry anomalies: %d, want 2 (%+v)", kinds[AnomalyMalformedEntry], c.Anomalies)
	}
	for _, a := range c.Anomalies {
		if a.Kind == AnomalyMalformedEntry && a.Value == "" {
			t.Errorf("a malformed line was recorded without what was seen: %+v", a)
		}
	}
}

func TestCollect_CycleIsAnAnomaly(t *testing.T) {
	a := encodeULID(msHour04, "000000000000000A")
	b := encodeULID(msHour04, "000000000000000B")
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(a, b, "one", "1.0.0") + line(b, a, "two", "2.0.0"),
	})

	c := collectFrom(store, a)
	if len(c.Chain) != 2 {
		t.Fatalf("chain holds %d entries, want the two distinct ones", len(c.Chain))
	}
	if !hasAnomaly(c, AnomalyChainCycle, a) {
		t.Errorf("a cyclic chain produced anomalies %+v", c.Anomalies)
	}
}

func TestCollect_IgnoresNonPartitionFiles(t *testing.T) {
	leaf := encodeULID(msHour04, "0000000000000003")
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl":     line(leaf, "", "safegit", "0.25.0"),
		"write-failure.marker":    "2026-08-13T04:17:52.913Z\n",
		"notes.txt":               "anything at all\n",
		"2026-08-13T04.jsonl.bak": "garbage\n",
	})

	c := collectFrom(store, leaf)
	if len(c.Anomalies) != 0 {
		t.Errorf("files that are not partitions were read: %+v", c.Anomalies)
	}
	if len(c.Chain) != 1 {
		t.Fatalf("chain holds %d entries, want 1", len(c.Chain))
	}
}

// An anomaly copies at most maxAnomalyValueBytes out of the store, and says so.
// A torn line has no length limit and the capture is written into a database
// row, so an uncapped value is a partition-sized blob per record.
func TestCollect_AnomalyValuesAreTruncated(t *testing.T) {
	leaf := encodeULID(msHour04, "0000000000000003")
	torn := `{"id":"` + strings.Repeat("x", 5000) // never closed: not a conforming entry

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": torn + "\n" + line(leaf, "", "safegit", "0.25.0"),
	})

	c := collectFrom(store, leaf)
	var seen bool
	for _, a := range c.Anomalies {
		if a.Kind != AnomalyMalformedEntry {
			continue
		}
		seen = true
		if len(a.Value) != maxAnomalyValueBytes {
			t.Errorf("the recorded value is %d bytes, want the first %d", len(a.Value), maxAnomalyValueBytes)
		}
		if !strings.Contains(a.Detail, "truncated") {
			t.Errorf("a truncated value was recorded without saying so: %q", a.Detail)
		}
	}
	if !seen {
		t.Fatalf("the torn line was not recorded: %+v", c.Anomalies)
	}

	// The same cap governs the variable's own value, which is recorded verbatim.
	polluted := strings.Repeat("p", 4096)
	c = collectFrom(store, polluted)
	if len(c.Anomalies) != 1 || c.Anomalies[0].Kind != AnomalyMalformedParentValue {
		t.Fatalf("the polluted variable produced anomalies %+v", c.Anomalies)
	}
	if len(c.Anomalies[0].Value) != maxAnomalyValueBytes {
		t.Errorf("the recorded variable is %d bytes, want the first %d",
			len(c.Anomalies[0].Value), maxAnomalyValueBytes)
	}
}

// A partition of malformed lines produces one anomaly per line. Uncapped, that
// is a multi-megabyte blob written into every record of the invocation, so the
// capture keeps the first maxAnomalies and states how many it dropped.
func TestCollect_AnomalyCountIsCapped(t *testing.T) {
	const malformed = 200
	leaf := encodeULID(msHour04, "0000000000000003")

	var content strings.Builder
	for i := 0; i < malformed; i++ {
		content.WriteString("{not an entry at all}\n")
	}
	content.WriteString(line(leaf, "", "safegit", "0.25.0"))

	store := writeStore(t, map[string]string{"2026-08-13T04.jsonl": content.String()})

	c := collectFrom(store, leaf)
	if len(c.Chain) != 1 {
		t.Fatalf("the well-formed entry did not resolve: %+v", c.Chain)
	}
	if len(c.Anomalies) != maxAnomalies+1 {
		t.Fatalf("the capture holds %d anomalies, want %d kept plus the dropped-count line",
			len(c.Anomalies), maxAnomalies)
	}
	last := c.Anomalies[len(c.Anomalies)-1]
	if last.Kind != AnomalyAnomaliesDropped {
		t.Fatalf("the last anomaly is %q, want the dropped-count line", last.Kind)
	}
	if !strings.Contains(last.Detail, fmt.Sprint(malformed-maxAnomalies)) {
		t.Errorf("the dropped-count line does not name how many were dropped: %q", last.Detail)
	}
	for _, a := range c.Anomalies[:maxAnomalies] {
		if a.Kind != AnomalyMalformedEntry {
			t.Errorf("a kept anomaly is %q, want the malformed lines that were seen first", a.Kind)
		}
	}

	// Nothing was dropped: no synthetic line at all, so a full list reads as
	// complete.
	clean := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(leaf, "", "safegit", "0.25.0"),
	})
	if c := collectFrom(clean, leaf); len(c.Anomalies) != 0 {
		t.Errorf("a clean capture recorded anomalies: %+v", c.Anomalies)
	}
}

// Nothing in the entry rules bounds a value's length: a conforming line may
// carry a five-megabyte app, and the chain is copied into every record. Each
// unbounded field is capped, and the capping is recorded.
func TestCollect_OversizedEntryFieldsAreCapped(t *testing.T) {
	huge := strings.Repeat("A", 5*1024*1024)
	leaf := encodeULID(msHour07, "0000000000000003")
	root := encodeULID(msHour04, "0000000000000001")

	// The leaf carries an oversized app and command; the root an oversized
	// version. parent_id is a string like any other until it is walked.
	leafLine := `{"id":"` + leaf + `","parent_id":"` + root + `","app":"` + huge +
		`","version":"0.25.0","command":"` + huge + `","dry_run":false,` +
		`"machine_mode":false,"quiet":false,"verbose":true,"approve_consequential":true,` +
		`"effect":"mutating","pid":48213,"spawned_at":"2026-08-13T04:17:52.913Z"}` + "\n"

	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": leafLine + line(root, "", "claudewheel", huge),
	})

	c := collectFrom(store, leaf)
	if len(c.Chain) != 2 {
		t.Fatalf("chain holds %d entries, want 2: %+v", len(c.Chain), c.Anomalies)
	}
	if len(c.Chain[0].App) != maxEntryFieldBytes {
		t.Errorf("the embedded app is %d bytes, want the first %d", len(c.Chain[0].App), maxEntryFieldBytes)
	}
	if c.Chain[0].Command == nil || len(*c.Chain[0].Command) != maxEntryFieldBytes {
		t.Errorf("the embedded command was not capped: %v", c.Chain[0].Command)
	}
	if len(c.Chain[1].Version) != maxEntryFieldBytes {
		t.Errorf("the embedded version is %d bytes, want the first %d", len(c.Chain[1].Version), maxEntryFieldBytes)
	}

	// The origin the record keeps is the capped name, not a five-megabyte one.
	if name, _ := c.Origin(); name == nil || len(*name) != maxEntryFieldBytes {
		t.Errorf("the origin name was not capped: %v", name)
	}

	fields := map[string]bool{}
	for _, a := range c.Anomalies {
		if a.Kind != AnomalyOversizedField {
			t.Errorf("unexpected anomaly %+v", a)
			continue
		}
		if len(a.Value) != maxAnomalyValueBytes {
			t.Errorf("the anomaly value is %d bytes, want the first %d", len(a.Value), maxAnomalyValueBytes)
		}
		for _, field := range []string{"app", "command", "version"} {
			if strings.Contains(a.Detail, "an entry's "+field+" ") {
				fields[field] = true
			}
		}
	}
	for _, field := range []string{"app", "command", "version"} {
		if !fields[field] {
			t.Errorf("the oversized %s was not recorded: %+v", field, c.Anomalies)
		}
	}
}

// An oversized parent_id is capped like every other string, and the capped value
// is then not a canonical identifier -- so the chain stops with the malformed
// entry recorded rather than embedding megabytes of it.
func TestCollect_OversizedParentIDIsCapped(t *testing.T) {
	huge := strings.Repeat("A", 5*1024*1024)
	leaf := encodeULID(msHour04, "0000000000000003")
	leafLine := `{"id":"` + leaf + `","parent_id":"` + huge +
		`","app":"safegit","version":"0.25.0","command":"push","dry_run":false,` +
		`"machine_mode":false,"quiet":false,"verbose":true,"approve_consequential":true,` +
		`"effect":"mutating","pid":48213,"spawned_at":"2026-08-13T04:17:52.913Z"}` + "\n"

	c := collectFrom(writeStore(t, map[string]string{"2026-08-13T04.jsonl": leafLine}), leaf)
	if len(c.Chain) != 1 {
		t.Fatalf("chain holds %d entries, want 1", len(c.Chain))
	}
	if c.Chain[0].ParentID == nil || len(*c.Chain[0].ParentID) != maxEntryFieldBytes {
		t.Fatalf("the embedded parent_id was not capped: %d bytes", len(*c.Chain[0].ParentID))
	}
	var oversized, malformed bool
	for _, a := range c.Anomalies {
		switch a.Kind {
		case AnomalyOversizedField:
			oversized = true
		case AnomalyMalformedEntry:
			malformed = true
		}
	}
	if !oversized || !malformed {
		t.Errorf("anomalies are %+v; want the capping and the unwalkable parent both recorded", c.Anomalies)
	}
}

// A partition is read whole, so its size is the reader's memory cost -- twice
// over, since the bytes are turned into a string. The specification's own worst
// case is the 8 MB roll threshold plus one hour of writes, so a file past
// maxPartitionBytes is not a partition this reader will load: it is recorded and
// skipped, rather than half-parsed into a chain nobody can trust.
func TestCollect_OversizedPartitionIsSkipped(t *testing.T) {
	leaf := encodeULID(msHour07, "0000000000000003")
	store := writeStore(t, map[string]string{
		"2026-08-13T04.jsonl": line(leaf, "", "safegit", "0.25.0"),
	})

	// Sparse: the file reports its size without occupying it.
	oversized := filepath.Join(store, "2026-08-13T09.jsonl")
	f, err := os.Create(oversized)
	if err != nil {
		t.Fatalf("creating the oversized partition: %v", err)
	}
	if err := f.Truncate(maxPartitionBytes + 1); err != nil {
		f.Close()
		t.Fatalf("sizing the oversized partition: %v", err)
	}
	f.Close()

	// An identifier in the oversized file's range: the search lands there first,
	// then walks back into the file that can be read.
	c := collectFrom(store, encodeULID(msHour09, "0000000000000009"))
	if !hasAnomaly(c, AnomalyStoreUnreadable, oversized) {
		t.Fatalf("the oversized partition was not recorded: %+v", c.Anomalies)
	}
	for _, a := range c.Anomalies {
		if a.Kind == AnomalyStoreUnreadable && a.Value == oversized &&
			!strings.Contains(a.Detail, fmt.Sprint(maxPartitionBytes)) {
			t.Errorf("the anomaly does not name the ceiling it hit: %q", a.Detail)
		}
		if a.Kind == AnomalyMalformedEntry {
			t.Errorf("the oversized partition was parsed anyway: %+v", a)
		}
	}

	// The readable partitions are unaffected.
	if c := collectFrom(store, leaf); len(c.Chain) != 1 {
		t.Errorf("the readable partition did not resolve: %+v", c)
	}
}

func hasAnomaly(c *Capture, kind, value string) bool {
	for _, a := range c.Anomalies {
		if a.Kind == kind && a.Value == value {
			return true
		}
	}
	return false
}
