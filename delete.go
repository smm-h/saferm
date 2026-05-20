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
	"github.com/smm-h/saferm/internal/config"
	"github.com/smm-h/saferm/internal/db"
	gitutil "github.com/smm-h/saferm/internal/git"
	"github.com/smm-h/saferm/internal/meta"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerDeleteCmd(app *strictcli.App) {
	app.Command("delete", "Archive files safely (instead of rm)", handleDelete,
		strictcli.WithFlags(
			strictcli.BoolFlag("recursive", "Allow deletion of directories", strictcli.Short("r")),
			strictcli.BoolFlag("force", "Ignore nonexistent files, skip prompts", strictcli.Short("f")),
			strictcli.BoolFlag("interactive", "Prompt before each deletion", strictcli.Short("i")),
			strictcli.StringFlag("description", "Why this deletion is happening"),
			strictcli.StringFlag("command", "The original bash command that triggered this", strictcli.Default("")),
			strictcli.StringFlag("meta", "Additional metadata as key=value", strictcli.Repeatable(), strictcli.Default(nil)),
			strictcli.BoolFlag("no-git", "Do not update the git index after archiving"),
		),
		strictcli.WithArgs(
			strictcli.NewArg("files", "Files or directories to archive", strictcli.Variadic()),
		),
	)
}

func handleDelete(kwargs map[string]interface{}) int {
	recursive := kwargs["recursive"].(bool)
	force := kwargs["force"].(bool)
	interactive := kwargs["interactive"].(bool)
	description := kwargs["description"].(string)
	command := kwargs["command"].(string)
	noGit := kwargs["no_git"].(bool)
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

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading config: %s\n", err)
		return ExitGeneral
	}

	if err := config.EnsureDirectories(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return ExitGeneral
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return ExitDatabase
	}
	defer database.Close()

	// Collect metadata
	metadata, err := meta.Collect(cfg, customMeta)
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
			if force {
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

		result, err := archive.Archive(absPath, cfg.ArchiveDir, recursive)
		if err != nil {
			if force && (err == archive.ErrFileNotFound) {
				continue
			}
			if err == archive.ErrRecursiveRequired {
				fmt.Fprintf(os.Stderr, "error: %s is a directory; use -r to delete recursively\n", file)
				return ExitUsage
			}
			fmt.Fprintf(os.Stderr, "error: archiving %s: %s\n", file, err)
			return ExitArchive
		}

		// Determine if it was a directory: files are stored as UUID, dirs as UUID.tar.zst
		archivePath := filepath.Join(cfg.ArchiveDir, result.UUID)
		_, statErr := os.Stat(archivePath + ".tar.zst")
		isDir := statErr == nil

		// Stage removal in git index if the file was tracked.
		if !noGit && metadata.GitRoot != "" && gitutil.IsGitTracked(absPath) {
			if err := gitutil.GitRmCached(absPath, isDir); err != nil {
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
			IsDirectory:  isDir,
			DeletedAt:    time.Now(),
			Command:      command,
			Description:  description,
			Metadata:     string(metaJSON),
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
