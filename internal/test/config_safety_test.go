package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// writeConfig writes a config.toml into the test SAFERM_HOME (homeDir/.saferm).
func writeConfig(t *testing.T, homeDir, content string) {
	t.Helper()
	configPath := filepath.Join(homeDir, ".saferm", "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing config.toml: %v", err)
	}
}

// TestConfigSafety_MalformedConfig_HardError verifies that a malformed
// config.toml is a HARD error (exit 1) with a parse position on stderr, rather
// than being silently ignored while saferm runs with default paths.
//
// RED against strictcli v0.17.0 (which prints "warning: invalid TOML ... ignoring"
// and continues with defaults -> the command succeeds, exit 0). GREEN against
// v0.21.0 (malformed config -> exit 1 with "config file ... (line N, column M)").
//
// This is a config-LAYER error owned by strictcli: exit code is EXACTLY 1, not
// saferm's ExitConflict (7) which is reserved for saferm's own semantic
// conflicts.
func TestConfigSafety_MalformedConfig_HardError(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	// Unterminated string literal: unambiguously malformed TOML.
	writeConfig(t, homeDir, "archive_dir = \"no closing quote\ndb_path = \"/x\"\n")

	// Drive a non-config command so the parse error is a hard abort.
	_, stderr, code := runSaferm(t, homeDir, "delete",
		"--description", "malformed config test",
		"-f", "nonexistent-malformed-config-test-file")

	if code != 1 {
		t.Fatalf("malformed config: expected exit 1, got %d (stderr=%q)", code, stderr)
	}
	// Parse position must be reported.
	if !strings.Contains(stderr, "config file") ||
		!strings.Contains(stderr, "line") ||
		!strings.Contains(stderr, "column") {
		t.Errorf("malformed config: expected parse position (config file/line/column) on stderr, got:\n%s", stderr)
	}
}

// TestConfigSafety_UnknownKey_HardError verifies that an unknown/typo'd key in
// config.toml is rejected (exit 1) instead of being silently ignored.
//
// Must drive a NON-config command: config subcommands (config show/set/...) skip
// unknown-key validation by design, so they would never go green.
//
// RED against v0.17.0 (no unknown-key validation -> key ignored -> exit 0).
// GREEN against v0.21.0 once ConfigFields for archive_dir/db_path are declared.
func TestConfigSafety_UnknownKey_HardError(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	// "archve_dir" is a typo of "archive_dir".
	writeConfig(t, homeDir, "archve_dir = \"/some/path\"\n")

	_, stderr, code := runSaferm(t, homeDir, "delete",
		"--description", "unknown key test",
		"-f", "nonexistent-unknown-key-test-file")

	if code != 1 {
		t.Fatalf("unknown key: expected exit 1, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "archve_dir") {
		t.Errorf("unknown key: expected stderr to name the offending key %q, got:\n%s", "archve_dir", stderr)
	}
}

// TestConfigSafety_DivergentArchiveDir_HardError verifies that when --archive-dir
// is supplied on the CLI AND archive_dir is set in config.toml to a DIFFERENT
// value, saferm errors (exit 1) instead of silently letting the CLI win.
//
// The parse aborts before any handler runs, so there are no DB/archive
// assertions -- only exit code + stderr.
//
// RED against v0.17.0 (cli-wins -> CLI archive-dir used -> exit 0). The CLI path
// is chosen under the test home so it is creatable, ensuring v0.17.0 reaches
// exit 0 rather than failing on directory creation.
// GREEN against v0.21.0 once archive-dir carries ConflictMode("error").
func TestConfigSafety_DivergentArchiveDir_HardError(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	cliArchive := filepath.Join(homeDir, "cli-archive")
	configArchive := filepath.Join(homeDir, "config-archive")
	// Clearly different literal paths.
	writeConfig(t, homeDir, "archive_dir = \""+configArchive+"\"\n")

	_, stderr, code := runSaferm(t, homeDir, "delete",
		"--archive-dir", cliArchive,
		"--description", "divergent archive-dir test",
		"-f", "nonexistent-divergent-test-file")

	if code != 1 {
		t.Fatalf("divergent archive-dir: expected exit 1, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "archive-dir") {
		t.Errorf("divergent archive-dir: expected stderr to mention archive-dir conflict, got:\n%s", stderr)
	}
}

// TestConfigSafety_IdenticalArchiveDir_Guard_ExitZero is a GREEN REGRESSION
// GUARD (passes on BOTH v0.17.0 and v0.21.0): byte-identical config + CLI
// archive-dir values must NOT be treated as a conflict. Divergence-aware
// conflict mode fires only when the values differ.
func TestConfigSafety_IdenticalArchiveDir_Guard_ExitZero(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	// Byte-identical literal used in both config and CLI. Creatable under the
	// test home so the delete handler (reached on both versions) succeeds.
	shared := filepath.Join(homeDir, "shared-archive")
	writeConfig(t, homeDir, "archive_dir = \""+shared+"\"\n")

	_, stderr, code := runSaferm(t, homeDir, "delete",
		"--archive-dir", shared,
		"--description", "identical archive-dir guard",
		"-f", "nonexistent-identical-guard-file")

	if code != 0 {
		t.Fatalf("identical archive-dir guard: expected exit 0, got %d (stderr=%q)", code, stderr)
	}
}

// TestConfigSafety_ExcludeEnvPatternsOverlap_Guard_ExitZero is a GREEN
// REGRESSION GUARD (passes on BOTH v0.17.0 and v0.21.0): exclude-env-patterns is
// deliberately left at cli-wins (no ConflictMode, no ConfigField -- it is a list
// type). Supplying it on the CLI while config also defines it (with an
// overlapping value) must stay exit 0, cli-wins.
func TestConfigSafety_ExcludeEnvPatternsOverlap_Guard_ExitZero(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	writeConfig(t, homeDir, "exclude_env_patterns = [\"(?i)token\", \"(?i)secret\"]\n")

	_, stderr, code := runSaferm(t, homeDir, "delete",
		"--exclude-env-patterns", "(?i)token",
		"--description", "exclude-env-patterns overlap guard",
		"-f", "nonexistent-exclude-guard-file")

	if code != 0 {
		t.Fatalf("exclude-env-patterns overlap guard: expected exit 0, got %d (stderr=%q)", code, stderr)
	}
}
