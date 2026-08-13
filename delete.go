package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smm-h/saferm/internal/archive"
	"github.com/smm-h/saferm/internal/db"
	gitutil "github.com/smm-h/saferm/internal/git"
	"github.com/smm-h/saferm/internal/meta"
	"github.com/smm-h/strictcli/go/strictcli"
)

// The two error modes `delete` accepts, spelled once.
const (
	onErrorAbort    = "abort"
	onErrorContinue = "continue"
)

func registerDeleteCmd(app *strictcli.App) {
	app.Command("delete", "Move files to the saferm archive with metadata tracking", handleDelete,
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithGrants(strictcli.Grant{
			Name:   "git-index",
			Reason: "a tracked file that moved into the archive must leave the git index too, or the next commit resurrects it",
			Kind:   strictcli.ProcMutate,
		}),
		strictcli.WithFlags(
			strictcli.BoolFlag("recursive", "Allow recursive deletion of directories and all their contents", strictcli.Short("r"), strictcli.Default(false)),
			strictcli.BoolFlag("ignore-missing", "Silently skip files that do not exist instead of erroring", strictcli.Short("f"), strictcli.Default(false)),
			strictcli.BoolFlag("interactive", "Prompt for confirmation before archiving each file", strictcli.Short("i"), strictcli.Default(false)),
			strictcli.StringFlag("description", "Mandatory explanation of why this deletion is happening"),
			strictcli.StringFlag("command", "Record the original rm command being replaced by saferm", strictcli.Default("")),
			strictcli.StringFlag("meta", "Attach additional metadata as key=value pairs (repeatable)", strictcli.Repeatable(), strictcli.Unique(false), strictcli.Default(nil)),
			strictcli.BoolFlag("update-git-index", "Run git rm --cached to stage removal in the git index", strictcli.Default(true)),
			// Mandatory, with no default. A batch that meets a bad path has two
			// defensible answers and they suit opposite callers: a script wants
			// the batch to stop before it does more, an interactive cleanup
			// wants the remaining paths archived anyway. Choosing one silently
			// would be wrong for the other half of the callers, so saferm
			// refuses to choose.
			strictcli.StringFlag("on-error",
				"What to do when a path cannot be archived: abort (stop at the first failure) or continue (archive the remaining paths, report every failure, and exit non-zero at the end). Mandatory: there is no default",
				strictcli.Choices(onErrorAbort, onErrorContinue)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("files", "One or more files or directories to move into the archive", strictcli.Variadic()),
		),
	)
}

func handleDelete(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	recursive := kwargs["recursive"].(bool)
	ignoreMissing := kwargs["ignore_missing"].(bool)
	interactive := kwargs["interactive"].(bool)
	description := kwargs["description"].(string)
	command := kwargs["command"].(string)
	updateGitIndex := kwargs["update_git_index"].(bool)
	onError := kwargs["on_error"].(string)
	metaValues := kwargs["meta"].([]interface{})
	filesRaw := kwargs["files"].([]interface{})
	verbose := ctx.Verbose()
	dryRun := ctx.DryRun()
	fx := ctx.Effects()

	if len(filesRaw) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files specified")
		return strictcli.Exit(ExitUsage)
	}

	// Parse --meta key=value pairs
	customMeta := make(map[string]string)
	for _, v := range metaValues {
		s := v.(string)
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --meta value %q must be in key=value format\n", s)
			return strictcli.Exit(ExitUsage)
		}
		customMeta[key] = value
	}

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(fx, filepath.Dir(archiveDir), archiveDir, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return strictcli.Exit(ExitGeneral)
	}

	// nil means "no archive yet", which only a dry run can see. Nothing below
	// touches the database in dry mode, so there is nothing to say about it.
	database, err := openArchiveDB(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(dbExit(err))
	}
	if database != nil {
		defer database.Close()
	}

	// Extract exclude patterns from args
	rawPatterns := kwargs["exclude_env_patterns"].([]interface{})
	patterns := make([]string, len(rawPatterns))
	for i, p := range rawPatterns {
		patterns[i] = p.(string)
	}

	// Collect metadata
	metadata, err := meta.Collect(patterns, customMeta)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: collecting metadata: %s\n", err)
		return strictcli.Exit(ExitGeneral)
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: serializing metadata: %s\n", err)
		return strictcli.Exit(ExitGeneral)
	}

	// Who ran this deletion, derived from the process trace store the metadata
	// capture already resolved. Both values are nil unless an entry resolved,
	// which is what "no tool claimed this" means -- there is no flag to state
	// an origin and nothing is inferred from any other variable.
	originName, originVersion := metadata.Trace.Origin()

	// One identifier for the whole invocation, minted unconditionally: every
	// record written below carries it, so a batch stays recoverable as a batch.
	groupID := archive.NewUUID()

	run := &deleteRun{
		ctx:            ctx,
		fx:             fx,
		database:       database,
		archiveDir:     archiveDir,
		originName:     originName,
		originVersion:  originVersion,
		groupID:        groupID,
		recursive:      recursive,
		ignoreMissing:  ignoreMissing,
		interactive:    interactive,
		updateGitIndex: updateGitIndex,
		verbose:        verbose,
		dryRun:         dryRun,
		description:    description,
		command:        command,
		metaJSON:       string(metaJSON),
		gitRoot:        metadata.GitRoot,
		scanner:        bufio.NewScanner(os.Stdin),
	}

	archived := 0
	failed := 0
	firstFailure := ExitSuccess

	for _, fileRaw := range filesRaw {
		ok, code := run.archiveOne(fileRaw.(string))
		if ok {
			archived++
		}
		if code == ExitSuccess {
			continue
		}
		failed++
		if firstFailure == ExitSuccess {
			firstFailure = code
		}
		// abort: stop here. Everything archived so far is already committed and
		// its identifiers are already on stdout, so the caller loses nothing by
		// the exit -- which is exactly why this mode is safe to offer.
		if onError == onErrorAbort {
			return strictcli.Exit(code)
		}
	}

	if archived > 0 && !verbose {
		if dryRun {
			say(ctx, "%d file(s) would be archived\n", archived)
		} else {
			say(ctx, "%d file(s) archived\n", archived)
		}
	}

	if failed > 0 {
		// continue mode: every failure was reported as it happened, and the
		// count is repeated at the end because the per-path lines are far up
		// the stream by now. The exit code is the FIRST failure's, so a caller
		// reading only the code learns what went wrong first rather than last.
		fmt.Fprintf(os.Stderr, "error: %d of %d path(s) failed; --on-error %s archived the rest\n",
			failed, len(filesRaw), onErrorContinue)
		return strictcli.Exit(firstFailure)
	}

	return strictcli.Exit(ExitSuccess)
}

// deleteRun is everything one `delete` invocation established before it began
// walking its paths: the open archive, the flags, and the metadata blob every
// record it writes will carry.
type deleteRun struct {
	ctx            *strictcli.Context
	fx             *strictcli.Effects
	database       *db.DB
	archiveDir     string
	recursive      bool
	ignoreMissing  bool
	interactive    bool
	updateGitIndex bool
	verbose        bool
	dryRun         bool
	description    string
	command        string
	metaJSON       string
	gitRoot        string
	scanner        *bufio.Scanner

	// originName and originVersion name the tool that ran this invocation, as
	// resolved from the trace store; both nil when none did.
	originName    *string
	originVersion *string

	// groupID is stamped on every record this invocation writes.
	groupID string
}

// archiveOne archives a single path, reporting its own failures on stderr.
//
// It returns whether a record was created (a path the caller declined
// interactively, or skipped under --ignore-missing, creates none and is not a
// failure either) and the exit code the failure deserves, or ExitSuccess. The
// caller decides what a failure means for the rest of the batch -- that is
// --on-error's whole job -- so nothing here ends the command.
func (r *deleteRun) archiveOne(file string) (archived bool, code int) {
	absPath, err := filepath.Abs(file)
	if err != nil {
		if r.ignoreMissing {
			return false, ExitSuccess
		}
		fmt.Fprintf(os.Stderr, "error: resolving path %q: %s\n", file, err)
		return false, ExitGeneral
	}

	if r.interactive {
		fmt.Fprintf(os.Stderr, "delete %s? [y/N] ", absPath)
		if !r.scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.scanner.Text())), "y") {
			return false, ExitSuccess
		}
	}

	// Plan first: everything up to the point of no return is reads, so it
	// runs identically in both modes and the preview is built from the
	// same facts the real archival uses.
	plan, err := archive.NewPlan(absPath, r.archiveDir, r.recursive)
	if err != nil {
		if r.ignoreMissing && (err == archive.ErrFileNotFound) {
			return false, ExitSuccess
		}
		if err == archive.ErrRecursiveRequired {
			fmt.Fprintf(os.Stderr, "error: %s is a directory; use -r to delete recursively\n", file)
			return false, ExitUsage
		}
		fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
		return false, ExitArchive
	}

	if r.dryRun {
		if err := recordArchival(r.ctx, r.fx, plan); err != nil {
			fmt.Fprintf(os.Stderr, "error: recording archival of %s: %s\n", file, err)
			return false, ExitArchive
		}
		if r.verbose {
			say(r.ctx, "would archive: %s\n", absPath)
		}
		return true, ExitSuccess
	}

	// The archive entry is written first and the source is left alone: the
	// removal below happens only once the record exists, so an insert that
	// fails can discard the entry and leave the path untouched. See
	// archive.Execute.
	result, err := archive.Execute(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
		return false, ExitArchive
	}

	rec := &db.DeletionRecord{
		UUID:          result.UUID,
		OriginalPath:  absPath,
		OriginalName:  filepath.Base(absPath),
		Size:          result.Size,
		Hash:          result.Hash,
		IsDirectory:   result.IsDirectory,
		DeletedAt:     time.Now(),
		Command:       r.command,
		Description:   r.description,
		Metadata:      r.metaJSON,
		OriginName:    r.originName,
		OriginVersion: r.originVersion,
		GroupID:       &r.groupID,
	}
	if result.IsSymlink {
		rec.SymlinkTarget = &result.SymlinkTarget
	}

	id, err := r.database.Insert(rec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: inserting database record for %s: %s\n", absPath, err)
		// Nothing has happened to the caller's path yet, so the archival can be
		// taken back whole: drop the entry and say so. A discard that itself
		// fails is the one remaining way to leave a blob with no row, so it is
		// reported loudly and by name rather than swallowed.
		if derr := archive.DiscardBlob(plan); derr != nil {
			fmt.Fprintf(os.Stderr, "error: removing the unrecorded archive entry %s: %s; it is an orphaned copy no saferm command can name\n",
				plan.Dest, derr)
		} else {
			fmt.Fprintf(os.Stderr, "note: %s was left in place; nothing was archived\n", absPath)
		}
		return false, dbExit(err)
	}

	// The record exists from here on, so the archived copy is findable by both
	// identifiers whatever happens next. Removing the source is the second half
	// of the archival: a failure here leaves a recorded deletion whose original
	// is still on disk, which is worth an error and is not silent data loss.
	if err := archive.RemoveSource(plan); err != nil {
		return false, reportUnremovedSource(absPath, plan, id, result.UUID, err)
	}

	// Stage removal in git index if the file was tracked.
	if r.updateGitIndex && r.gitRoot != "" && gitutil.IsGitTracked(absPath) {
		if err := gitutil.GitRmCached(absPath, result.IsDirectory); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git rm --cached failed for %s: %s\n", file, err)
		} else if r.verbose {
			say(r.ctx, "Staged removal in git: %s\n", file)
		}
	}

	// Both identifiers, one line per record, in every mode but --quiet.
	// The numeric id is the counter of this one database; the uuid names
	// the archived entry itself and is the handle that survives -- `info`,
	// `undelete` and `purge` all accept it. Printing them here is what
	// spares a caller from running `list` afterwards and guessing which row
	// was its own, and it is why an abort partway through a multi-path
	// delete still leaves the caller holding the identifiers of everything
	// that did get archived.
	say(r.ctx, "archived: [%d] %s %s (%s)\n", id, result.UUID, absPath, humanSize(result.Size))

	return true, ExitSuccess
}

// reportUnremovedSource explains a [archive.RemoveSource] that refused or
// failed, and returns the exit code for it.
//
// The record is already committed by the time this runs, so every branch states
// two things: what the record holds, and what the path holds, because after
// this failure they are no longer the same thing and only saying one of them
// would be a half-truth.
//
// Two of the branches are about the ENTRY rather than the source: it can be
// destroyed by a concurrent purge that selected the row this delete had just
// inserted, or replaced by something saferm never archived. Neither removes
// anything -- there is nothing to take back in the first case and nothing
// saferm owns in the second -- and neither claims the record holds the archived
// content, because in both it does not.
//
// The ErrArchivedContentChanged branch is the only one that undoes anything. A file's archive
// entry is a hard link, so a write through the original path rewrites the
// archived bytes too: the row says one hash and the blob has another, and no
// re-reading fixes that, because the content the row describes is gone from the
// machine. Keeping the entry would leave a permanent lie in the archive AND a
// second name for a file the caller is still editing, so it is discarded, which
// costs nothing -- dropping one of two links to an inode leaves the caller's
// file exactly where it is, with the newer content it now has. What survives is
// a row naming no blob, which `list` still shows and `purge` can clear, and
// which is honest about the one thing that must not be got wrong: nothing was
// destroyed.
func reportUnremovedSource(absPath string, plan *archive.Plan, id int64, uuid string, err error) int {
	switch {
	case errors.Is(err, archive.ErrArchivedContentChanged):
		if derr := archive.DiscardBlob(plan); derr != nil {
			fmt.Fprintf(os.Stderr, "error: removing the archive entry %s, which no longer matches record [%d] %s: %s; it is now a second name for %s and a write to either changes both\n",
				plan.Dest, id, uuid, derr, absPath)
		}
		fmt.Fprintf(os.Stderr, "error: %s was written to while it was being archived: %s; it was left in place with its current content, the archived copy was discarded because record [%d] %s records the hash it had before the write, and that row now names nothing -- purge it and run the delete again\n",
			absPath, err, id, uuid)
		return ExitArchive

	case errors.Is(err, archive.ErrDirectoryChanged):
		// The tree grew or was written into after the tar was closed, so the
		// archive does not cover what os.RemoveAll would destroy. Same remedy as
		// a file written through: the tree is left whole -- including the part
		// nothing archived -- and the incomplete archive is discarded rather
		// than left standing under a row that claims to hold the whole tree.
		if derr := archive.DiscardBlob(plan); derr != nil {
			fmt.Fprintf(os.Stderr, "error: removing the archive entry %s, which does not hold all of %s: %s; it is an incomplete copy under record [%d] %s\n",
				plan.Dest, absPath, derr, id, uuid)
		}
		fmt.Fprintf(os.Stderr, "error: %s changed while it was being archived: %s; the tree was left whole, the incomplete archive was discarded, and record [%d] %s now names nothing -- purge it and run the delete again\n",
			absPath, err, id, uuid)
		return ExitArchive

	case errors.Is(err, archive.ErrSourceReplaced), errors.Is(err, archive.ErrSourceDiverged):
		fmt.Fprintf(os.Stderr, "error: not removing %s: %s; record [%d] %s holds the content that was archived, and %s now holds something else -- neither was destroyed\n",
			absPath, err, id, uuid, absPath)
		return ExitArchive

	case errors.Is(err, archive.ErrArchiveEntryMissing):
		// The row was committed and then its blob went -- a concurrent purge
		// selecting the row saferm had just inserted does exactly this. There is
		// nothing to discard and nothing to undo: the row names nothing, and the
		// path the caller asked to delete is now the only copy of its content.
		fmt.Fprintf(os.Stderr, "error: not removing %s: %s; record [%d] %s was committed before the entry disappeared, so that row names nothing and %s is the only copy of its content left -- purge the row and run the delete again\n",
			absPath, err, id, uuid, absPath)
		return ExitArchive

	case errors.Is(err, archive.ErrArchiveEntryReplaced):
		// The entry is still a file, but not the one that was archived, so
		// saferm did not put it there and does not remove it: discarding it
		// would destroy something whose origin is unknown. The row is left
		// standing over it and is worth nothing, which is what the message
		// says rather than claiming the record holds the archived content.
		fmt.Fprintf(os.Stderr, "error: not removing %s: %s; record [%d] %s names an archive entry that is no longer what was archived, so that row names nothing saferm can vouch for -- the entry was left alone because saferm did not put it there, and %s was not destroyed\n",
			absPath, err, id, uuid, absPath)
		return ExitArchive

	default:
		fmt.Fprintf(os.Stderr, "error: removing %s after archiving it: %s; record [%d] %s holds the archived copy\n",
			absPath, err, id, uuid)
		return ExitArchive
	}
}

// recordArchival mints the mutations an archival performs onto the effects
// handle, so that a dry run's would-do log names every path that would move or
// disappear.
//
// It runs in dry mode only. It is the ONE place in saferm where the record and
// the execution are separate calls, and it is deliberate: archiving is a
// compound operation -- hash, then a hard link with a copy-and-verify fallback
// where the link is refused, or tar + zstd of a whole tree, and then a separate
// removal of the original once the deletion is recorded -- and the effects
// handle's closed method set has no primitive for a streaming archive, a
// verified copy or a hard link. So the handle carries the description and
// archive.Execute carries the act; the dry-mode branch in the caller is what
// keeps the two from ever both happening.
//
// Every kind is described the same way, as the two things that actually happen:
// an archive entry appears, and the source goes. The file case used to mint
// `rename`, which was true when a file was archived by renaming it and has not
// been since. A rename says one atomic move whose destination then holds the
// only copy; the real sequence is a link, a database insert, and an unlink,
// which leaves the file readable at both names in between and can end with the
// source still in place. A preview that says `rename` is previewing a saferm
// that no longer exists, so it says instead exactly what the two seam calls do.
//
// The archive directory itself is not minted here: ensureDirectories already
// declared it once, before the first plan was built, and repeating it per file
// would pad the would-do log with a line that says nothing new.
func recordArchival(ctx *strictcli.Context, fx *strictcli.Effects, plan *archive.Plan) error {
	// Structural, not decorative: the content below is a description of an
	// entry, not the bytes anything writes, and outside dry mode the handle
	// would write it. Nothing calls this outside dry mode; this is what keeps
	// that true.
	if !ctx.DryRun() {
		return errors.New("recordArchival describes an archival for a preview and must not run outside --dry-run")
	}

	// What the entry will contain, per kind:
	//
	//   - A symlink's entry IS its target path written out, so the real content
	//     is right here.
	//   - A regular file's entry is a hard link to the source (or a verified
	//     copy), so its size is the source's size. The buffer carries that
	//     length and nothing else: the framework renders a write's content as
	//     its byte count, and a preview minted with no content at all claimed
	//     "(0 bytes)" for every file however large -- a promise to write an
	//     empty file. Reading the file to say the same number would cost a full
	//     read, and a preview must not cost what the operation it previews does
	//     not.
	//   - A directory's entry is a .tar.zst that does not exist until the
	//     compression runs, and its size is not knowable before it does. The
	//     write is declared with no content, which is the only thing that is
	//     true about it here; strictcli's write has no way to say "size
	//     unknown", so the log renders that absence as 0.
	var content []byte
	switch plan.Kind {
	case archive.KindSymlink:
		content = []byte(plan.SymlinkTarget)
	case archive.KindFile:
		info, err := os.Lstat(plan.Source)
		if err != nil {
			return err
		}
		content = make([]byte, info.Size())
	}
	if _, err := fx.Write(plan.Dest, content, strictcli.Resource("saferm-entry:"+plan.UUID)); err != nil {
		return err
	}
	_, err := fx.Remove(plan.Source, strictcli.Resource("path:"+plan.Source))
	return err
}
