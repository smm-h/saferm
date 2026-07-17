package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/saferm/internal/meta"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerInfoCmd(app *strictcli.App) {
	app.Command("info", "Show full metadata for a deletion", handleInfo,
		strictcli.WithArgs(
			strictcli.NewArg("id", "ID of the archived item"),
		),
	)
}

func handleInfo(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	idStr := kwargs["id"].(string)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %q is not a valid ID\n", idStr)
		return strictcli.Exit(ExitUsage)
	}

	dbPath := kwargs["db_path"].(string)

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(ExitDatabase)
	}
	defer database.Close()

	rec, err := database.QueryByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "error: no record with ID %d\n", id)
			return strictcli.Exit(ExitFileNotFound)
		}
		fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
		return strictcli.Exit(ExitDatabase)
	}

	fileType := "file"
	if rec.SymlinkTarget != nil {
		fileType = "symlink"
	} else if rec.IsDirectory {
		fileType = "directory"
	}

	fmt.Printf("ID:            %d\n", rec.ID)
	fmt.Printf("UUID:          %s\n", rec.UUID)
	fmt.Printf("Original Path: %s\n", rec.OriginalPath)
	fmt.Printf("Original Name: %s\n", rec.OriginalName)
	fmt.Printf("Size:          %s (%d bytes)\n", humanSize(rec.Size), rec.Size)
	fmt.Printf("Hash:          %s\n", rec.Hash)
	fmt.Printf("Type:          %s\n", fileType)
	if rec.SymlinkTarget != nil {
		fmt.Printf("Target:        %s\n", *rec.SymlinkTarget)
	}
	fmt.Printf("Deleted At:    %s\n", rec.DeletedAt.Format(time.RFC3339))
	fmt.Printf("Description:   %s\n", rec.Description)
	if rec.Command != "" {
		fmt.Printf("Command:       %s\n", rec.Command)
	}

	if rec.RestoredAt != nil {
		fmt.Printf("Restored At:   %s\n", rec.RestoredAt.Format(time.RFC3339))
	}
	if rec.RestoredTo != nil {
		fmt.Printf("Restored To:   %s\n", *rec.RestoredTo)
	}
	if rec.PurgedAt != nil {
		fmt.Printf("Purged At:     %s\n", rec.PurgedAt.Format(time.RFC3339))
	}

	// Parse and display metadata
	if rec.Metadata != "" {
		var m meta.Metadata
		if err := json.Unmarshal([]byte(rec.Metadata), &m); err == nil {
			fmt.Println("\nMetadata:")
			if m.GitBranch != "" {
				fmt.Printf("  Git Branch:  %s\n", m.GitBranch)
			}
			if m.GitHEAD != "" {
				fmt.Printf("  Git HEAD:    %s\n", m.GitHEAD)
			}
			if m.GitRoot != "" {
				fmt.Printf("  Git Root:    %s\n", m.GitRoot)
			}
			if m.PPID != 0 {
				fmt.Printf("  Parent PID:  %d\n", m.PPID)
			}
			if m.ParentCmd != "" {
				fmt.Printf("  Parent Cmd:  %s\n", m.ParentCmd)
			}
			if len(m.Custom) > 0 {
				fmt.Println("  Custom:")
				for k, v := range m.Custom {
					fmt.Printf("    %s = %s\n", k, v)
				}
			}
			if len(m.Env) > 0 {
				fmt.Printf("  Environment: %d variables captured\n", len(m.Env))
			}
		}
	}

	return strictcli.Exit(ExitSuccess)
}
