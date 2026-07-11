package main

import (
	"os"
	"path/filepath"

	"github.com/smm-h/strictcli/go/strictcli"
)

// baseDir returns the saferm base directory. If SAFERM_HOME is set, its value
// is used as-is (expected to be an absolute path). Otherwise falls back to
// ~/.saferm/.
func baseDir() string {
	if override := os.Getenv("SAFERM_HOME"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".saferm")
}

func main() {
	base := baseDir()

	app := strictcli.NewApp("saferm", version, "AI-first safe rm replacement",
		strictcli.WithEnvPrefix("SAFERM"),
		strictcli.WithConfig(),
		strictcli.WithConfigPath(filepath.Join(base, "config.toml")),
		strictcli.WithConfigFormat("toml"),
	)

	app.GlobalFlag(strictcli.BoolFlag("verbose", "Enable verbose output", strictcli.Default(false)))
	app.GlobalFlag(strictcli.StringFlag("archive-dir", "Path to the archive directory",
		strictcli.Default(filepath.Join(base, "archive")),
		strictcli.ConflictMode("error")))
	app.GlobalFlag(strictcli.StringFlag("db-path", "Path to the SQLite database",
		strictcli.Default(filepath.Join(base, "db", "saferm.db")),
		strictcli.ConflictMode("error")))
	// exclude-env-patterns is intentionally left at cli-wins (no ConflictMode) and
	// gets no ConfigField: it is a list, and ConfigField supports only scalars.
	app.GlobalFlag(strictcli.StringFlag("exclude-env-patterns", "Regex patterns for env vars to exclude from metadata",
		strictcli.Repeatable(), strictcli.Unique(true),
		strictcli.Default([]interface{}{"(?i)token", "(?i)secret", "(?i)password", "(?i)key", "(?i)credential"})))

	// ConfigFields for the two scalar config-managed flags. Each names an existing
	// flag param, so it renders once (as a "-- help" annotation on the flag line in
	// `config show`) and feeds unknown-key validation. The flags carry defaults, so
	// these fields declare NO default (one-absent-OK), avoiding duplication of the
	// runtime-computed SAFERM_HOME-derived paths. The help text records the
	// infrastructure-vs-config boundary.
	app.ConfigField("archive_dir", strictcli.ConfigFieldHelp(
		"Directory where deleted files are archived. Default derives from SAFERM_HOME "+
			"(an infrastructure override: unlike config values, SAFERM_HOME is NOT suppressed by --hermetic)."))
	app.ConfigField("db_path", strictcli.ConfigFieldHelp(
		"Path to the SQLite database. Default derives from SAFERM_HOME "+
			"(an infrastructure override: unlike config values, SAFERM_HOME is NOT suppressed by --hermetic)."))

	registerDeleteCmd(app)
	registerUndeleteCmd(app)
	registerListCmd(app)
	registerPurgeCmd(app)
	registerInfoCmd(app)

	app.Run()
}
