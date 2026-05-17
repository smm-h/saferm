// Package archive handles file and directory archival to the saferm archive.
// It supports atomic same-filesystem renames, cross-device copy-and-verify,
// and tar+zstd compression for directories.
package archive
