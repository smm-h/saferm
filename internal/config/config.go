package config

import (
	"os"
	"path/filepath"
)

// Config holds saferm configuration, loaded from TOML.
type Config struct {
	ArchiveDir         string   `toml:"archive_dir"`
	DBPath             string   `toml:"db_path"`
	ExcludeEnvPatterns []string `toml:"exclude_env_patterns"`
}

// Load reads the config file and returns a Config with defaults applied.
// Stub: returns defaults for now.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Config{
		ArchiveDir: filepath.Join(home, ".saferm", "archive"),
		DBPath:     filepath.Join(home, ".saferm", "db", "saferm.db"),
		ExcludeEnvPatterns: []string{
			"*TOKEN*",
			"*SECRET*",
			"*PASSWORD*",
			"*KEY*",
			"*CREDENTIAL*",
		},
	}, nil
}

// EnsureDirectories creates the archive and database directories if they
// do not exist. Stub: not yet implemented.
func EnsureDirectories(_ *Config) error {
	return nil
}
