// Package trace reads the strictcli process trace store, so a deletion can
// record who ran it.
//
// The store is a shared, append-only JSONL record of process ancestry: at the
// seam where one command-line tool spawns another, the spawning invocation
// writes one line describing itself and hands that line's identifier to the
// child in STRICTCLI_TRACE_PARENT. saferm is a consumer, never a writer. It
// parses the variable and reads the store itself, per the normative
// specification (strictcli's docs/process-trace-store.md), because the
// framework deliberately exposes no accessor for the ancestry stack -- nothing
// in the framework may branch on data no framework code reads.
//
// Everything here is observational. A capture cannot fail a deletion: an absent
// variable, a polluted one, a pruned store, a torn line and a parent that
// resolves to nothing are all legal states, each recorded as an anomaly and
// carried on from. Consumers noticing dangling parents is the store's primary
// failure-detection channel, which is why the anomalies are written into the
// record rather than discarded.
//
// A capture resolves the FULL ancestry chain at capture time and keeps the
// flattened entries, not only their identifiers, so the record stays
// self-contained: age-based pruning of the store can never orphan it. The
// identifiers are kept alongside for correlation with whatever store data still
// exists.
package trace
