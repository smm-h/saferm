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

	app.GlobalFlag(strictcli.BoolFlag("verbose", "Enable verbose output"))
	app.GlobalFlag(strictcli.StringFlag("archive-dir", "Path to the archive directory",
		strictcli.Default(filepath.Join(base, "archive"))))
	app.GlobalFlag(strictcli.StringFlag("db-path", "Path to the SQLite database",
		strictcli.Default(filepath.Join(base, "db", "saferm.db"))))
	app.GlobalFlag(strictcli.StringFlag("exclude-env-patterns", "Regex patterns for env vars to exclude from metadata",
		strictcli.Repeatable(), strictcli.Unique(true),
		strictcli.Default([]interface{}{"(?i)token", "(?i)secret", "(?i)password", "(?i)key", "(?i)credential"})))

	registerDeleteCmd(app)
	registerUndeleteCmd(app)
	registerListCmd(app)
	registerPurgeCmd(app)
	registerInfoCmd(app)

	app.Run()
}
