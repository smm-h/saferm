package archive

// ArchiveResult holds the outcome of archiving a file or directory.
type ArchiveResult struct {
	UUID string
	Hash string
	Size int64
}

// Archive compresses and stores a file or directory in the archive.
// Stub: not yet implemented.
func Archive(path string, archiveDir string) (*ArchiveResult, error) {
	return nil, nil
}

// Restore extracts an archived file or directory to the given destination.
// Stub: not yet implemented.
func Restore(uuid string, archiveDir string, destPath string) error {
	return nil
}
