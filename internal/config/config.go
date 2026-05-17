package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds saferm configuration, loaded from TOML.
type Config struct {
	ArchiveDir         string   `toml:"archive_dir"`
	DBPath             string   `toml:"db_path"`
	ExcludeEnvPatterns []string `toml:"exclude_env_patterns"`
}

// defaultExcludeEnvPatterns are regex patterns matching environment variable
// names that should be excluded from metadata capture.
var defaultExcludeEnvPatterns = []string{
	"(?i)token",
	"(?i)secret",
	"(?i)password",
	"(?i)key",
	"(?i)credential",
}

// BaseDir returns the saferm base directory. If SAFERM_HOME is set, its value
// is used as-is (expected to be an absolute path). Otherwise falls back to
// ~/.saferm/.
func BaseDir() string {
	if override := os.Getenv("SAFERM_HOME"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to HOME env var if UserHomeDir fails
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".saferm")
}

// DefaultConfig returns a Config with all defaults filled in.
func DefaultConfig() *Config {
	base := BaseDir()
	patterns := make([]string, len(defaultExcludeEnvPatterns))
	copy(patterns, defaultExcludeEnvPatterns)
	return &Config{
		ArchiveDir:         filepath.Join(base, "archive"),
		DBPath:             filepath.Join(base, "db", "saferm.db"),
		ExcludeEnvPatterns: patterns,
	}
}

// Load reads the config file from the default location (~/.saferm/config.toml)
// and returns a Config with defaults applied. If the file doesn't exist,
// returns the default config without error.
func Load() (*Config, error) {
	return LoadFrom(filepath.Join(BaseDir(), "config.toml"))
}

// LoadFrom reads config from the specified path. If the file doesn't exist,
// returns the default config without error. If it exists but is malformed,
// returns an error.
func LoadFrom(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Expand ~ in paths
	cfg.ArchiveDir = expandHome(cfg.ArchiveDir)
	cfg.DBPath = expandHome(cfg.DBPath)

	return cfg, nil
}

// EnsureDirectories creates the archive dir, db dir (parent of DBPath),
// and base dir if they don't exist. Uses 0700 permissions.
func EnsureDirectories(cfg *Config) error {
	dirs := []string{
		BaseDir(),
		cfg.ArchiveDir,
		filepath.Dir(cfg.DBPath),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
