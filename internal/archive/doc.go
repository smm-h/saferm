// Package archive handles file and directory archival to the saferm archive.
// It supports same-filesystem hard links, copy-and-verify wherever a link is
// refused, and tar+zstd compression for directories.
//
// Archival is two calls, not one: [Execute] writes the archive entry and leaves
// the original alone, [RemoveSource] removes the original afterwards, and
// [DiscardBlob] takes the entry back. The gap between them is where a caller
// records the deletion, so an archive entry never exists without a way to find
// it.
package archive
