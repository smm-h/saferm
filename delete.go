package main

import (
	"bufio"
	"encoding/json"
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

	scanner := bufio.NewScanner(os.Stdin)
	archived := 0

	for _, fileRaw := range filesRaw {
		file := fileRaw.(string)

		// Resolve to absolute path
		absPath, err := filepath.Abs(file)
		if err != nil {
			if ignoreMissing {
				continue
			}
			fmt.Fprintf(os.Stderr, "error: resolving path %q: %s\n", file, err)
			return strictcli.Exit(ExitGeneral)
		}

		if interactive {
			fmt.Fprintf(os.Stderr, "delete %s? [y/N] ", absPath)
			if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
				continue
			}
		}

		// Plan first: everything up to the point of no return is reads, so it
		// runs identically in both modes and the preview is built from the
		// same facts the real archival uses.
		plan, err := archive.NewPlan(absPath, archiveDir, recursive)
		if err != nil {
			if ignoreMissing && (err == archive.ErrFileNotFound) {
				continue
			}
			if err == archive.ErrRecursiveRequired {
				fmt.Fprintf(os.Stderr, "error: %s is a directory; use -r to delete recursively\n", file)
				return strictcli.Exit(ExitUsage)
			}
			fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
			return strictcli.Exit(ExitArchive)
		}

		if dryRun {
			if err := recordArchival(fx, plan); err != nil {
				fmt.Fprintf(os.Stderr, "error: recording archival of %s: %s\n", file, err)
				return strictcli.Exit(ExitArchive)
			}
			archived++
			if verbose {
				say(ctx, "would archive: %s\n", absPath)
			}
			continue
		}

		result, err := archive.Execute(plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
			return strictcli.Exit(ExitArchive)
		}

		// Stage removal in git index if the file was tracked.
		if updateGitIndex && metadata.GitRoot != "" && gitutil.IsGitTracked(absPath) {
			if err := gitutil.GitRmCached(absPath, result.IsDirectory); err != nil {
				fmt.Fprintf(os.Stderr, "warning: git rm --cached failed for %s: %s\n", file, err)
			} else if verbose {
				say(ctx, "Staged removal in git: %s\n", file)
			}
		}

		rec := &db.DeletionRecord{
			UUID:         result.UUID,
			OriginalPath: absPath,
			OriginalName: filepath.Base(absPath),
			Size:         result.Size,
			Hash:         result.Hash,
			IsDirectory:  result.IsDirectory,
			DeletedAt:    time.Now(),
			Command:      command,
			Description:  description,
			Metadata:     string(metaJSON),
		}
		if result.IsSymlink {
			rec.SymlinkTarget = &result.SymlinkTarget
		}

		id, err := database.Insert(rec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: inserting database record: %s\n", err)
			return strictcli.Exit(dbExit(err))
		}

		// Both identifiers, one line per record, in every mode but --quiet.
		// The numeric id is the counter of this one database; the uuid names
		// the archived entry itself and is the handle that survives -- `info`,
		// `undelete` and `purge` all accept it. Printing them here is what
		// spares a caller from running `list` afterwards and guessing which row
		// was its own, and it is why an abort partway through a multi-path
		// delete still leaves the caller holding the identifiers of everything
		// that did get archived.
		say(ctx, "archived: [%d] %s %s (%s)\n", id, result.UUID, absPath, humanSize(result.Size))

		archived++
	}

	if archived > 0 && !verbose {
		if dryRun {
			say(ctx, "%d file(s) would be archived\n", archived)
		} else {
			say(ctx, "%d file(s) archived\n", archived)
		}
	}

	return strictcli.Exit(ExitSuccess)
}

// recordArchival mints the mutations an archival performs onto the effects
// handle, so that a dry run's would-do log names every path that would move or
// disappear.
//
// It runs in dry mode only. It is the ONE place in saferm where the record and
// the execution are separate calls, and it is deliberate: archiving is a
// compound operation --
// hash, then rename with a copy-and-verify fallback across devices, or tar +
// zstd of a whole tree followed by a recursive removal -- and the effects
// handle's closed method set has no primitive for a streaming archive or a
// verified copy. Minting `rename` for the file case would silently drop the
// cross-device fallback. So the handle carries the description and
// archive.Execute carries the act; the dry-mode branch in the caller is what
// keeps the two from ever both happening.
// The archive directory itself is not minted here: ensureDirectories already
// declared it once, before the first plan was built, and repeating it per file
// would pad the would-do log with a line that says nothing new.
func recordArchival(fx *strictcli.Effects, plan *archive.Plan) error {
	switch plan.Kind {
	case archive.KindDirectory:
		// tar + zstd of the tree, then the tree itself goes.
		if _, err := fx.Write(plan.Dest, []byte{}, strictcli.Resource("saferm-entry:"+plan.UUID)); err != nil {
			return err
		}
		_, err := fx.Remove(plan.Source, strictcli.Resource("path:"+plan.Source))
		return err
	case archive.KindSymlink:
		if _, err := fx.Write(plan.Dest, []byte(plan.SymlinkTarget), strictcli.Resource("saferm-entry:"+plan.UUID)); err != nil {
			return err
		}
		_, err := fx.Remove(plan.Source, strictcli.Resource("path:"+plan.Source))
		return err
	default:
		_, err := fx.Rename(plan.Source, plan.Dest, strictcli.Resource("saferm-entry:"+plan.UUID))
		return err
	}
}
