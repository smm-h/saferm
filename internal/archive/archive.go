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
	"time"

	"github.com/klauspost/compress/zstd"
)

// Sentinel errors.
var (
	ErrFileNotFound      = errors.New("file not found")
	ErrRecursiveRequired = errors.New("target is a directory; recursive flag required")
	ErrConflict          = errors.New("destination already exists")
	ErrHashMismatch      = errors.New("hash mismatch after copy")

	// The ways a source can stop being what was archived while the caller is
	// recording the deletion. See [RemoveSource] for why that window is wide
	// enough to matter and what each of these means for the record.
	ErrSourceReplaced         = errors.New("the source path no longer names the file that was archived")
	ErrArchiveEntryMissing    = errors.New("the archived copy is gone, so nothing holds the content the removal would destroy")
	ErrArchiveEntryReplaced   = errors.New("the archive entry is no longer the file that was archived")
	ErrSourceDiverged         = errors.New("the source changed after it was hashed, and the archive holds an independent copy of the older content")
	ErrArchivedContentChanged = errors.New("the source was written through while its archive entry was a link to it, so the recorded hash no longer describes the archived bytes")
	ErrDirectoryChanged       = errors.New("the tree changed after it was archived, so the archive does not hold everything the removal would destroy")
	ErrNotExecuted            = errors.New("the plan was never executed, so there is nothing to check the source against")
)

// linkFile is os.Link, indirected so a test can make the link fail.
//
// The copy-and-verify fallback runs only when the kernel or the filesystem
// refuses a hard link -- a different device, protected_hardlinks, a filesystem
// with no links, an inode at its link limit -- and a test process can provoke
// none of those on a temp directory it owns. Without this seam the fallback is
// reachable only in production, which is the one place it must not be first
// exercised.
var linkFile = os.Link

// ArchiveResult holds the outcome of archiving a file or directory.
type ArchiveResult struct {
	UUID          string
	Hash          string
	Size          int64
	IsSymlink     bool
	SymlinkTarget string
	IsDirectory   bool
}

// Kind names what an archival is about to move.
type Kind int

// The three shapes an archived entry takes on disk.
const (
	KindFile Kind = iota
	KindDirectory
	KindSymlink
)

// Plan is everything an archival can determine by reading: what the entry is,
// where it will land, and (for a symlink) what it points at. Building one
// mutates nothing, so a caller can render a plan as a preview and stop, or hand
// it to [Execute] and go through with it.
type Plan struct {
	Source        string
	ArchiveDir    string
	UUID          string
	Kind          Kind
	Dest          string
	SymlinkTarget string

	// What [Execute] saw, and what [RemoveSource] checks the path against
	// before it destroys anything. identity is the source's own os.FileInfo as
	// of the archival, so it carries the dev/ino pair the identity check needs
	// and the size and mtime the content check needs; hash is what was
	// recorded for a file; linked says whether the archive entry is the
	// source's own inode or an independent copy, which is what decides the
	// meaning of a content change.
	identity os.FileInfo
	hash     string
	linked   bool

	// What went into the tar, for a directory: every path the archiving walk
	// wrote, with the size and mtime it had when it was written. A tree's
	// identity is one inode and says nothing about its contents, so this is the
	// only thing that can tell [RemoveSource] whether the archive covers what
	// the recursive removal is about to destroy.
	members map[string]member
}

// member is one path as it went into a directory's tar: enough to notice, on
// the way back, that the tree no longer holds only what was archived.
type member struct {
	size    int64
	modTime time.Time
	regular bool
}

// NewPlan inspects path and resolves where archiving it would put it. It
// performs no mutation.
func NewPlan(path string, archiveDir string, isRecursive bool) (*Plan, error) {
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

	p := &Plan{Source: path, ArchiveDir: archiveDir, UUID: NewUUID()}
	switch {
	case info.IsDir():
		p.Kind = KindDirectory
		p.Dest = filepath.Join(archiveDir, p.UUID+".tar.zst")
	case info.Mode()&os.ModeSymlink != 0:
		p.Kind = KindSymlink
		p.Dest = filepath.Join(archiveDir, p.UUID+".symlink")
		target, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("reading symlink target: %w", err)
		}
		p.SymlinkTarget = target
	default:
		p.Kind = KindFile
		p.Dest = filepath.Join(archiveDir, p.UUID)
	}
	return p, nil
}

// Execute writes the archive entry a [Plan] describes, and LEAVES THE SOURCE
// WHERE IT IS.
//
// Archiving is deliberately split from removing the original, because between
// the two there is a third party: the caller's database, which is what makes an
// archived entry findable at all. Doing it in one step meant an archive that
// succeeded and a record that failed produced a blob nobody could name, a
// source path that was gone, and no way back -- and the live archive really did
// collect orphaned blobs that way. So the order is Execute, record, then
// [RemoveSource]; a record that fails calls [DiscardBlob] instead and the
// caller's file has never been touched.
//
// For a regular file the entry is a hard link to the original, which is why
// leaving the source in place costs nothing: the content is not copied and both
// names point at the same inode until RemoveSource drops one of them.
func Execute(p *Plan) (*ArchiveResult, error) {
	if err := os.MkdirAll(p.ArchiveDir, 0700); err != nil {
		return nil, fmt.Errorf("creating archive dir: %w", err)
	}
	switch p.Kind {
	case KindDirectory:
		return archiveDirectory(p)
	case KindSymlink:
		return archiveSymlink(p)
	default:
		return archiveFile(p)
	}
}

// RemoveSource removes the original an executed [Plan] archived. It is the
// second half of an archival and runs only once the entry is recorded.
//
// It removes by identity, not by name, and only once it has seen the archived
// copy. Between [Execute] and this call sits the caller's database insert, and
// that is not an instant: a contended SQLite write retries for tens of seconds,
// and both the source path and the archive entry are live filesystem paths the
// whole time. Three things can happen in there, and removing whatever the name
// happens to resolve to gets all three wrong:
//
//   - The path can be REPLACED -- renamed over, or removed and recreated. The
//     archive holds the original; the name now leads somewhere else, and
//     removing it would destroy a file nothing archived.
//   - A regular file's archive entry is a hard link, so a write THROUGH the
//     path mutates the archived bytes. The recorded hash then describes content
//     that no longer exists anywhere, and removing the source would leave that
//     record standing over a blob it does not match.
//   - The ENTRY can go, or stop being the archived thing. The row is inserted
//     before this runs, so a concurrent purge can select it and destroy its
//     blob perfectly legitimately; removing the source afterwards leaves no
//     copy of the content anywhere at all.
//
// So both sides are re-checked first and the removal is refused on any
// mismatch, with [ErrSourceReplaced], [ErrSourceDiverged],
// [ErrArchivedContentChanged], [ErrArchiveEntryMissing] or
// [ErrArchiveEntryReplaced] naming which one the caller is holding. Nothing is
// undone here: refusing to remove is the whole of the remedy this half can
// apply, and what to do about the record is the recording caller's decision.
func RemoveSource(p *Plan) error {
	if err := verifySource(p); err != nil {
		return err
	}
	if p.Kind == KindDirectory {
		return os.RemoveAll(p.Source)
	}
	return os.Remove(p.Source)
}

// verifySource reports whether the source is still the thing [Execute]
// archived AND the archived copy is still there to hold it.
//
// Both halves are checked for every kind, because the removal is irreversible
// for every kind and either half alone lets it through wrongly:
//
//   - The SOURCE's identity, by dev/ino. A directory or a symlink has no
//     content of its own that the archive shares, so being the same inode is
//     most of the question for them; a regular file is also checked for
//     content, and cheaply -- the size and mtime as of the hash against the
//     current stat, and a re-hash only when those differ, rather than
//     re-reading every archived file on every delete. A write that restores
//     both size and mtime is not detected; that is the one hole left open
//     deliberately, and it costs a full re-read to close.
//   - The ENTRY's existence. The record is inserted before this runs, which is
//     what makes the archived copy findable -- and findable by a concurrent
//     `saferm purge --all` too, which will legitimately select that row and
//     destroy its blob while this archival is still waiting on its own insert.
//     Nothing else on the machine holds a copied file's bytes, a tree's
//     .tar.zst or a symlink's recorded target, so removing the source with the
//     entry gone destroys the content outright.
//
// The entry check is an Lstat, not a Stat, and the difference is the whole
// point for the linked case: a Stat follows a symlink, so an entry replaced by
// a link back at the source would satisfy os.SameFile and let the removal
// proceed -- destroying the source and leaving the "archive entry" dangling.
func verifySource(p *Plan) error {
	if p.identity == nil {
		return ErrNotExecuted
	}

	cur, err := os.Lstat(p.Source)
	if err != nil {
		return fmt.Errorf("re-checking %s before removing it: %w", p.Source, err)
	}
	if !os.SameFile(cur, p.identity) {
		return fmt.Errorf("%s: %w", p.Source, ErrSourceReplaced)
	}

	entry, err := os.Lstat(p.Dest)
	if err != nil {
		return fmt.Errorf("%s: %w (%v)", p.Dest, ErrArchiveEntryMissing, err)
	}

	if p.Kind == KindDirectory {
		return verifyTreeUnchanged(p)
	}
	if p.Kind != KindFile {
		return nil
	}

	if p.linked {
		// The exact form of the check, available only while the entry and the
		// source are one inode: whatever else moved, these two must still be
		// the same file, or the archive is not holding what is about to be
		// destroyed.
		if !os.SameFile(cur, entry) {
			return fmt.Errorf("the archive entry %s is no longer the same file as %s: %w", p.Dest, p.Source, ErrArchiveEntryReplaced)
		}
	}

	if cur.Size() == p.identity.Size() && cur.ModTime().Equal(p.identity.ModTime()) {
		return nil
	}
	hash, err := hashFile(p.Source)
	if err != nil {
		return fmt.Errorf("re-hashing %s: %w", p.Source, err)
	}
	if hash == p.hash {
		// Touched, but the bytes are the ones that were recorded.
		return nil
	}
	if p.linked {
		return fmt.Errorf("%s: %w", p.Source, ErrArchivedContentChanged)
	}
	return fmt.Errorf("%s: %w", p.Source, ErrSourceDiverged)
}

// verifyTreeUnchanged reports whether the tree still holds only what the tar
// holds. A directory's identity check is its top-level inode and covers none of
// this: a file written INTO the tree after the tar was closed is in no archive
// anywhere, and os.RemoveAll destroys it along with everything else.
//
// So the tree is walked once more against the member list the archiving walk
// recorded, and two findings refuse the removal: a path the tar does not have
// at all, and a regular file whose size or mtime no longer matches what went
// into it -- the same cheap comparison the single-file case makes before it
// re-hashes, minus the re-hash, because re-reading a whole tree to catch a
// same-size same-mtime rewrite would cost a second full pass over it.
//
// A path the tar has and the tree no longer does is NOT a refusal: the archive
// then holds more than the tree, which is the state a completed archival aims
// for anyway.
//
// The residual window is real and deliberate: a write landing between this walk
// and the os.RemoveAll that follows it is not seen. That gap is microseconds
// wide, against the tens of seconds the database insert can hold open, and
// closing it would need something the filesystem does not offer.
func verifyTreeUnchanged(p *Plan) error {
	var unarchived []string
	err := filepath.WalkDir(p.Source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		recorded, ok := p.members[path]
		if !ok {
			unarchived = append(unarchived, path)
			return nil
		}
		if !recorded.regular {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() != recorded.size || !info.ModTime().Equal(recorded.modTime) {
			unarchived = append(unarchived, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("re-walking %s before removing it: %w", p.Source, err)
	}
	if len(unarchived) > 0 {
		return fmt.Errorf("%s: %w: %s", p.Source, ErrDirectoryChanged, namePaths(unarchived))
	}
	return nil
}

// namePaths renders the paths a refusal is about. All of them up to a handful,
// because the caller has to go and look at them, and a count after that so a
// tree that changed wholesale does not print itself into the terminal.
func namePaths(paths []string) string {
	const shown = 5
	if len(paths) <= shown {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(paths[:shown], ", "), len(paths)-shown)
}

// DiscardBlob removes the archive entry [Execute] wrote, undoing it. The source
// is untouched by both calls, so a discarded archival leaves the filesystem
// exactly as it was.
func DiscardBlob(p *Plan) error {
	return os.Remove(p.Dest)
}

func archiveSymlink(p *Plan) (*ArchiveResult, error) {
	target, err := os.Readlink(p.Source)
	if err != nil {
		return nil, fmt.Errorf("reading symlink target: %w", err)
	}

	info, err := os.Lstat(p.Source)
	if err != nil {
		return nil, err
	}

	// Write the target path to a .symlink metadata file for defense-in-depth
	// recovery if the database is lost. The link itself stays until the caller
	// has recorded the entry; see [Execute].
	metaPath := filepath.Join(p.ArchiveDir, p.UUID+".symlink")
	if err := os.WriteFile(metaPath, []byte(target), 0600); err != nil {
		return nil, fmt.Errorf("writing symlink metadata: %w", err)
	}

	p.identity = info
	return &ArchiveResult{UUID: p.UUID, Hash: "", Size: 0, IsSymlink: true, SymlinkTarget: target}, nil
}

func archiveFile(p *Plan) (*ArchiveResult, error) {
	hash, err := hashFile(p.Source)
	if err != nil {
		return nil, fmt.Errorf("hashing file: %w", err)
	}

	// Taken after the hash, so the size and mtime it carries are the ones that
	// went with the bytes that were read. [verifySource] compares against them.
	info, err := os.Lstat(p.Source)
	if err != nil {
		return nil, err
	}
	size := info.Size()

	dst := filepath.Join(p.ArchiveDir, p.UUID)

	// A hard link, not a rename: the archive entry and the original are the
	// same inode until the caller has recorded the deletion and calls
	// [RemoveSource]. It costs one directory entry and no content copy, and it
	// is what lets a failed record undo itself with the source untouched.
	linked := true
	if err := linkFile(p.Source, dst); err != nil {
		if !linkRefused(err) {
			return nil, fmt.Errorf("linking file into archive: %w", err)
		}
		// The filesystem or the kernel's policy will not link this file --
		// a different device, a filesystem without hard links, or Linux's
		// protected_hardlinks refusing a file the caller does not own. Copying
		// and verifying the copy against the hash reaches the same state at the
		// cost of reading the file twice, which is what the cross-device path
		// has always done.
		if err := copyAndVerify(p.Source, dst, hash); err != nil {
			return nil, err
		}
		linked = false
	}

	p.identity = info
	p.hash = hash
	p.linked = linked
	return &ArchiveResult{UUID: p.UUID, Hash: hash, Size: size}, nil
}

func archiveDirectory(p *Plan) (*ArchiveResult, error) {
	path, archiveDir, uuid := p.Source, p.ArchiveDir, p.UUID

	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	// Walk tree to compute total size.
	var totalSize int64
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
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

	members, err := createTarZst(path, dst)
	if err != nil {
		// Clean up partial archive on failure.
		os.Remove(dst)
		return nil, fmt.Errorf("creating tar.zst: %w", err)
	}

	hash, err := hashFile(dst)
	if err != nil {
		os.Remove(dst)
		return nil, fmt.Errorf("hashing archive: %w", err)
	}

	// The tree itself stays until the caller has recorded the entry; see
	// [Execute]. It used to go here, which is why a failing record left a
	// compressed tree nobody could name and no directory to go back to.
	p.identity = info
	p.members = members
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

// NewUUID returns a UUID v4 string using crypto/rand.
//
// It names an archive entry, and it is also what mints the group identifier a
// delete invocation stamps on every record it writes: both are opaque handles
// minted with no coordination between processes, so they are the same thing.
func NewUUID() string {
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

// createTarZst creates a .tar.zst archive of srcDir at dstPath, and returns
// what it put in it: every walked path, with the size and mtime it had as it
// was written. That list is the only description of a tree's contents the
// archival produces, and [verifyTreeUnchanged] is what reads it.
func createTarZst(srcDir string, dstPath string) (map[string]member, error) {
	members := make(map[string]member)

	outFile, err := os.Create(dstPath)
	if err != nil {
		return nil, err
	}
	defer outFile.Close()

	zstWriter, err := zstd.NewWriter(outFile)
	if err != nil {
		return nil, err
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
		if archivePath == baseDir+"/"+"." {
			archivePath = baseDir
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Recorded before the content is written, not after: a file that is
		// rewritten WHILE it is being copied gets a member whose size and mtime
		// are the pre-copy ones, so the check on the way back refuses. Recording
		// afterwards would instead make the refusal disappear for exactly the
		// case that needs it.
		members[path] = member{size: info.Size(), modTime: info.ModTime(), regular: info.Mode().IsRegular()}

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
		return nil, err
	}

	// Close in reverse order.
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := zstWriter.Close(); err != nil {
		return nil, err
	}
	if err := outFile.Close(); err != nil {
		return nil, err
	}
	return members, nil
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

// linkRefused reports whether a failed os.Link means "this file cannot be hard
// linked here", as opposed to a genuine failure.
//
// The four refusals, all of which the copy-and-verify path handles correctly:
// EXDEV (source and archive on different filesystems), EPERM (Linux's
// protected_hardlinks refusing a link to a file the caller neither owns nor can
// write, and filesystems that reject links outright), EOPNOTSUPP/ENOSYS (a
// filesystem with no hard links at all) and EMLINK (the inode is already at its
// link limit). Anything else -- a missing archive directory, a full disk, a
// read-only mount -- is reported as the failure it is, because copying would
// fail the same way and hide the reason.
func linkRefused(err error) bool {
	if isCrossDevice(err) {
		return true
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		if sysErr, ok := linkErr.Err.(syscall.Errno); ok {
			switch sysErr {
			case syscall.EPERM, syscall.EOPNOTSUPP, syscall.ENOSYS, syscall.EMLINK:
				return true
			}
		}
	}
	return false
}
