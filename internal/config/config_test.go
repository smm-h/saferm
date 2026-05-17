package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ArchiveDir == "" {
		t.Fatal("ArchiveDir should not be empty")
	}
	if !strings.HasSuffix(cfg.ArchiveDir, filepath.Join(".saferm", "archive")) {
		t.Errorf("ArchiveDir should end with .saferm/archive, got %s", cfg.ArchiveDir)
	}

	if cfg.DBPath == "" {
		t.Fatal("DBPath should not be empty")
	}
	if !strings.HasSuffix(cfg.DBPath, filepath.Join(".saferm", "db", "saferm.db")) {
		t.Errorf("DBPath should end with .saferm/db/saferm.db, got %s", cfg.DBPath)
	}

	if len(cfg.ExcludeEnvPatterns) == 0 {
		t.Fatal("ExcludeEnvPatterns should have default entries")
	}
	// Verify patterns are regexes (contain (?i) for case-insensitive matching)
	for _, p := range cfg.ExcludeEnvPatterns {
		if !strings.Contains(p, "(?i)") {
			t.Errorf("pattern %q should be a case-insensitive regex", p)
		}
	}
}

func TestLoad_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.toml")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom should not error on missing file: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadFrom should return default config on missing file")
	}
	if cfg.ArchiveDir == "" {
		t.Error("default config should have ArchiveDir set")
	}
	if cfg.DBPath == "" {
		t.Error("default config should have DBPath set")
	}
	if len(cfg.ExcludeEnvPatterns) == 0 {
		t.Error("default config should have ExcludeEnvPatterns set")
	}
}

func TestLoad_ValidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
archive_dir = "/custom/archive"
db_path = "/custom/db/my.db"
exclude_env_patterns = ["(?i)api_key", "(?i)auth"]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if cfg.ArchiveDir != "/custom/archive" {
		t.Errorf("ArchiveDir = %q, want /custom/archive", cfg.ArchiveDir)
	}
	if cfg.DBPath != "/custom/db/my.db" {
		t.Errorf("DBPath = %q, want /custom/db/my.db", cfg.DBPath)
	}
	if len(cfg.ExcludeEnvPatterns) != 2 {
		t.Fatalf("ExcludeEnvPatterns len = %d, want 2", len(cfg.ExcludeEnvPatterns))
	}
	if cfg.ExcludeEnvPatterns[0] != "(?i)api_key" {
		t.Errorf("ExcludeEnvPatterns[0] = %q, want (?i)api_key", cfg.ExcludeEnvPatterns[0])
	}
}

func TestLoad_ValidTOML_TildeExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
archive_dir = "~/my-archive"
db_path = "~/my-db/saferm.db"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	home, _ := os.UserHomeDir()

	if cfg.ArchiveDir != filepath.Join(home, "my-archive") {
		t.Errorf("ArchiveDir = %q, want %s", cfg.ArchiveDir, filepath.Join(home, "my-archive"))
	}
	if cfg.DBPath != filepath.Join(home, "my-db", "saferm.db") {
		t.Errorf("DBPath = %q, want %s", cfg.DBPath, filepath.Join(home, "my-db", "saferm.db"))
	}
}

func TestLoad_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
archive_dir = [[[invalid toml
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("LoadFrom should return error on malformed TOML")
	}
}

func TestEnsureDirectories(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		ArchiveDir: filepath.Join(dir, "saferm", "archive"),
		DBPath:     filepath.Join(dir, "saferm", "db", "saferm.db"),
	}

	// Temporarily override BaseDir by testing that EnsureDirectories
	// creates the directories specified in cfg
	if err := EnsureDirectories(cfg); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	// Check archive dir exists
	info, err := os.Stat(cfg.ArchiveDir)
	if err != nil {
		t.Fatalf("archive dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("archive path is not a directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("archive dir permissions = %o, want 0700", info.Mode().Perm())
	}

	// Check db dir exists (parent of DBPath)
	dbDir := filepath.Dir(cfg.DBPath)
	info, err = os.Stat(dbDir)
	if err != nil {
		t.Fatalf("db dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("db path is not a directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("db dir permissions = %o, want 0700", info.Mode().Perm())
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo", "bar")},
		{"~/", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
	}

	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
