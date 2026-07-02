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
	app.Command("delete", "Archive files safely (instead of rm)", handleDelete,
		strictcli.WithFlags(
			strictcli.BoolFlag("recursive", "Allow deletion of directories", strictcli.Short("r"), strictcli.Default(false)),
			strictcli.BoolFlag("ignore-missing", "Ignore nonexistent files", strictcli.Short("f"), strictcli.Default(false)),
			strictcli.BoolFlag("interactive", "Prompt before each deletion", strictcli.Short("i"), strictcli.Default(false)),
			strictcli.StringFlag("description", "Why this deletion is happening"),
			strictcli.StringFlag("command", "The original bash command that triggered this", strictcli.Default("")),
			strictcli.StringFlag("meta", "Additional metadata as key=value", strictcli.Repeatable(), strictcli.Unique(false), strictcli.Default(nil)),
			strictcli.BoolFlag("update-git-index", "Update the git index after archiving", strictcli.Default(true)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("files", "Files or directories to archive", strictcli.Variadic()),
		),
	)
}

func handleDelete(kwargs map[string]interface{}) int {
	recursive := kwargs["recursive"].(bool)
	ignoreMissing := kwargs["ignore_missing"].(bool)
	interactive := kwargs["interactive"].(bool)
	description := kwargs["description"].(string)
	command := kwargs["command"].(string)
	updateGitIndex := kwargs["update_git_index"].(bool)
	metaValues := kwargs["meta"].([]interface{})
	filesRaw := kwargs["files"].([]interface{})
	verbose, _ := kwargs["verbose"].(bool)

	if len(filesRaw) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files specified")
		return ExitUsage
	}

	// Parse --meta key=value pairs
	customMeta := make(map[string]string)
	for _, v := range metaValues {
		s := v.(string)
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --meta value %q must be in key=value format\n", s)
			return ExitUsage
		}
		customMeta[key] = value
	}

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(baseDir(), archiveDir, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return ExitGeneral
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return ExitDatabase
	}
	defer database.Close()

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
		return ExitGeneral
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: serializing metadata: %s\n", err)
		return ExitGeneral
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
			return ExitGeneral
		}

		if interactive {
			fmt.Fprintf(os.Stderr, "delete %s? [y/N] ", absPath)
			if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
				continue
			}
		}

		result, err := archive.Archive(absPath, archiveDir, recursive)
		if err != nil {
			if ignoreMissing && (err == archive.ErrFileNotFound) {
				continue
			}
			if err == archive.ErrRecursiveRequired {
				fmt.Fprintf(os.Stderr, "error: %s is a directory; use -r to delete recursively\n", file)
				return ExitUsage
			}
			fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
			return ExitArchive
		}

		// Stage removal in git index if the file was tracked.
		if updateGitIndex && metadata.GitRoot != "" && gitutil.IsGitTracked(absPath) {
			if err := gitutil.GitRmCached(absPath, result.IsDirectory); err != nil {
				fmt.Fprintf(os.Stderr, "warning: git rm --cached failed for %s: %s\n", file, err)
			} else if verbose {
				fmt.Printf("Staged removal in git: %s\n", file)
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

		if _, err := database.Insert(rec); err != nil {
			fmt.Fprintf(os.Stderr, "error: inserting database record: %s\n", err)
			return ExitDatabase
		}

		if verbose {
			fmt.Printf("archived: %s (%s)\n", absPath, humanSize(result.Size))
		}

		archived++
	}

	if archived > 0 && !verbose {
		fmt.Printf("%d file(s) archived\n", archived)
	}

	return ExitSuccess
}
