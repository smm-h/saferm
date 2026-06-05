package archive

import (
	"archive/tar"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/klauspost/compress/zstd"
)

// Sentinel errors.
var (
	ErrFileNotFound     = errors.New("file not found")
	ErrRecursiveRequired = errors.New("target is a directory; recursive flag required")
	ErrConflict         = errors.New("destination already exists")
	ErrHashMismatch     = errors.New("hash mismatch after copy")
)

// ArchiveResult holds the outcome of archiving a file or directory.
type ArchiveResult struct {
	UUID          string
	Hash          string
	Size          int64
	IsSymlink     bool
	SymlinkTarget string
	IsDirectory   bool
}

// Archive moves a file or directory into archiveDir, returning the result.
// For files: moved directly (or copied cross-device) with SHA-256 hash.
// For directories: compressed into a .tar.zst archive.
func Archive(path string, archiveDir string, isRecursive bool) (*ArchiveResult, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}

	if info.IsDir() && !isRecursive {
		return nil, ErrRecursiveRequired
	}

	uuid := generateUUID()

	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		return nil, fmt.Errorf("creating archive dir: %w", err)
	}

	if info.IsDir() {
		return archiveDirectory(path, archiveDir, uuid)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return archiveSymlink(path, archiveDir, uuid)
	}
	return archiveFile(path, archiveDir, uuid)
}

func archiveSymlink(path string, archiveDir string, uuid string) (*ArchiveResult, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return nil, fmt.Errorf("reading symlink target: %w", err)
	}

	// Write the target path to a .symlink metadata file for defense-in-depth
	// recovery if the database is lost.
	metaPath := filepath.Join(archiveDir, uuid+".symlink")
	if err := os.WriteFile(metaPath, []byte(target), 0600); err != nil {
		return nil, fmt.Errorf("writing symlink metadata: %w", err)
	}

	if err := os.Remove(path); err != nil {
		// Clean up metadata file on failure.
		os.Remove(metaPath)
		return nil, fmt.Errorf("removing symlink: %w", err)
	}

	return &ArchiveResult{UUID: uuid, Hash: "", Size: 0, IsSymlink: true, SymlinkTarget: target}, nil
}

func archiveFile(path string, archiveDir string, uuid string) (*ArchiveResult, error) {
	hash, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("hashing file: %w", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	size := info.Size()

	dst := filepath.Join(archiveDir, uuid)

	err = os.Rename(path, dst)
	if err != nil {
		if isCrossDevice(err) {
			if err := copyAndVerify(path, dst, hash); err != nil {
				return nil, err
			}
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("removing original after cross-device copy: %w", err)
			}
		} else {
			return nil, fmt.Errorf("moving file to archive: %w", err)
		}
	}

	return &ArchiveResult{UUID: uuid, Hash: hash, Size: size}, nil
}

func archiveDirectory(path string, archiveDir string, uuid string) (*ArchiveResult, error) {
	// Walk tree to compute total size.
	var totalSize int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	dst := filepath.Join(archiveDir, uuid+".tar.zst")

	if err := createTarZst(path, dst); err != nil {
		// Clean up partial archive on failure.
		os.Remove(dst)
		return nil, fmt.Errorf("creating tar.zst: %w", err)
	}

	hash, err := hashFile(dst)
	if err != nil {
		os.Remove(dst)
		return nil, fmt.Errorf("hashing archive: %w", err)
	}

	// Only remove original after successful archive+hash.
	if err := os.RemoveAll(path); err != nil {
		return nil, fmt.Errorf("removing original directory: %w", err)
	}

	return &ArchiveResult{UUID: uuid, Hash: hash, Size: totalSize, IsDirectory: true}, nil
}

// Restore extracts an archived file or directory to destPath.
// When symlinkTarget is non-empty, the entry is restored as a symlink
// pointing to that target (no physical archive file is read).
func Restore(uuid string, archiveDir string, destPath string, isDirectory bool, force bool, symlinkTarget string) error {
	if _, err := os.Lstat(destPath); err == nil {
		if !force {
			return ErrConflict
		}
		if err := os.RemoveAll(destPath); err != nil {
			return fmt.Errorf("removing existing destination: %w", err)
		}
	}

	if symlinkTarget != "" {
		return restoreSymlink(uuid, archiveDir, destPath, symlinkTarget)
	}
	if isDirectory {
		return restoreDirectory(uuid, archiveDir, destPath)
	}
	return restoreFile(uuid, archiveDir, destPath)
}

func restoreSymlink(uuid string, archiveDir string, destPath string, target string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}
	if err := os.Symlink(target, destPath); err != nil {
		return fmt.Errorf("recreating symlink: %w", err)
	}
	// Clean up the .symlink metadata file from the archive.
	os.Remove(filepath.Join(archiveDir, uuid+".symlink"))
	return nil
}

func restoreFile(uuid string, archiveDir string, destPath string) error {
	src := filepath.Join(archiveDir, uuid)
	if _, err := os.Lstat(src); err != nil {
		return fmt.Errorf("archive entry not found: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	err := os.Rename(src, destPath)
	if err != nil {
		if isCrossDevice(err) {
			if err := copyFile(src, destPath); err != nil {
				return err
			}
			return os.Remove(src)
		}
		return fmt.Errorf("restoring file: %w", err)
	}
	return nil
}

func restoreDirectory(uuid string, archiveDir string, destPath string) error {
	src := filepath.Join(archiveDir, uuid+".tar.zst")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("archive entry not found: %w", err)
	}

	if err := extractTarZst(src, destPath); err != nil {
		return fmt.Errorf("extracting archive: %w", err)
	}

	return os.Remove(src)
}

// generateUUID returns a UUID v4 string using crypto/rand.
func generateUUID() string {
	var uuid [16]byte
	if _, err := io.ReadFull(rand.Reader, uuid[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	// Set version 4.
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant bits.
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// hashFile computes the SHA-256 hex digest of a file by streaming.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile copies src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return dstFile.Close()
}

// copyAndVerify copies a file and verifies the SHA-256 hash matches.
func copyAndVerify(src, dst string, expectedHash string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}

	hash, err := hashFile(dst)
	if err != nil {
		os.Remove(dst)
		return err
	}

	if hash != expectedHash {
		os.Remove(dst)
		return ErrHashMismatch
	}
	return nil
}

// createTarZst creates a .tar.zst archive of srcDir at dstPath.
func createTarZst(srcDir string, dstPath string) error {
	outFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zstWriter, err := zstd.NewWriter(outFile)
	if err != nil {
		return err
	}
	defer zstWriter.Close()

	tarWriter := tar.NewWriter(zstWriter)
	defer tarWriter.Close()

	baseDir := filepath.Base(srcDir)

	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute relative path within the archive.
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		// Prefix with the base directory name.
		archivePath := filepath.Join(baseDir, relPath)
		if archivePath == baseDir+"/" + "." {
			archivePath = baseDir
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Handle symlinks.
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			header := &tar.Header{
				Typeflag: tar.TypeSymlink,
				Name:     archivePath,
				Linkname: linkTarget,
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
			}
			return tarWriter.WriteHeader(header)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = archivePath

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if d.IsDir() || !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tarWriter, f)
		return err
	})
	if err != nil {
		return err
	}

	// Close in reverse order.
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := zstWriter.Close(); err != nil {
		return err
	}
	return outFile.Close()
}

// extractTarZst extracts a .tar.zst archive into dstDir.
func extractTarZst(srcPath string, dstDir string) error {
	inFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer inFile.Close()

	zstReader, err := zstd.NewReader(inFile)
	if err != nil {
		return err
	}
	defer zstReader.Close()

	tarReader := tar.NewReader(zstReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Security: prevent path traversal.
		cleanName := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanName, "/") || strings.HasPrefix(cleanName, "..") || strings.Contains(cleanName, "/../") {
			return fmt.Errorf("invalid tar entry path (path traversal): %s", header.Name)
		}

		// Strip the top-level directory from the path so contents extract
		// directly into dstDir.
		parts := strings.SplitN(filepath.ToSlash(cleanName), "/", 2)
		var targetRel string
		if len(parts) < 2 || parts[1] == "" {
			// This is the top-level directory entry itself; just ensure dstDir exists.
			if err := os.MkdirAll(dstDir, 0755); err != nil {
				return err
			}
			continue
		}
		targetRel = parts[1]

		target := filepath.Join(dstDir, targetRel)

		// Double-check the resolved path is inside dstDir.
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		absDst, err := filepath.Abs(dstDir)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(absTarget, absDst+string(filepath.Separator)) && absTarget != absDst {
			return fmt.Errorf("invalid tar entry path (escapes destination): %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

// isCrossDevice checks if an error is a cross-device link error (EXDEV).
func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		if sysErr, ok := linkErr.Err.(syscall.Errno); ok {
			return sysErr == syscall.EXDEV
		}
	}
	return false
}
