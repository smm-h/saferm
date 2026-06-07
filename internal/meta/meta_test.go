package meta

import (
	"os"
	"testing"
)

func TestCollectEnv_FiltersPatterns(t *testing.T) {
	// Set known env vars that should be filtered.
	t.Setenv("SAFERM_TEST_SECRET_KEY", "should-be-filtered")
	t.Setenv("SAFERM_TEST_API_TOKEN", "should-be-filtered")
	t.Setenv("SAFERM_TEST_PASSWORD_DB", "should-be-filtered")
	t.Setenv("SAFERM_TEST_NORMAL_VAR", "should-be-kept")

	patterns := []string{
		"(?i)secret",
		"(?i)token",
		"(?i)password",
	}

	result := collectEnv(patterns)

	if _, ok := result["SAFERM_TEST_SECRET_KEY"]; ok {
		t.Error("SECRET_KEY should be filtered out")
	}
	if _, ok := result["SAFERM_TEST_API_TOKEN"]; ok {
		t.Error("API_TOKEN should be filtered out")
	}
	if _, ok := result["SAFERM_TEST_PASSWORD_DB"]; ok {
		t.Error("PASSWORD_DB should be filtered out")
	}
	if v, ok := result["SAFERM_TEST_NORMAL_VAR"]; !ok || v != "should-be-kept" {
		t.Errorf("NORMAL_VAR should be kept with value 'should-be-kept', got %q", v)
	}
}

func TestCollectEnv_NoPatterns(t *testing.T) {
	t.Setenv("SAFERM_TEST_ANYTHING", "captured")

	result := collectEnv(nil)

	if v, ok := result["SAFERM_TEST_ANYTHING"]; !ok || v != "captured" {
		t.Errorf("with no patterns, all vars should be captured; SAFERM_TEST_ANYTHING = %q", v)
	}

	// Also check that PATH is present (always set in any environment).
	if _, ok := result["PATH"]; !ok {
		t.Error("PATH should be present when no patterns are used")
	}
}

func TestCollectGitContext(t *testing.T) {
	// saferm is itself a git repo, so this should return non-empty values
	// when run from the project directory.
	branch, head, root := collectGitContext()

	if branch == "" {
		t.Error("git branch should not be empty when running in a git repo")
	}
	if head == "" {
		t.Error("git HEAD should not be empty when running in a git repo")
	}
	if len(head) < 7 {
		t.Errorf("git HEAD should be a full SHA, got %q", head)
	}
	if root == "" {
		t.Error("git root should not be empty when running in a git repo")
	}
}

func TestCollectGitContext_NotARepo(t *testing.T) {
	// Run git commands from a directory that is not a git repo.
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	branch, head, root := collectGitContext()

	if branch != "" {
		t.Errorf("branch should be empty outside a repo, got %q", branch)
	}
	if head != "" {
		t.Errorf("HEAD should be empty outside a repo, got %q", head)
	}
	if root != "" {
		t.Errorf("root should be empty outside a repo, got %q", root)
	}
}

func TestCollectParentProcess(t *testing.T) {
	ppid, cmdline := collectParentProcess()

	if ppid <= 0 {
		t.Errorf("PPID should be > 0, got %d", ppid)
	}
	if cmdline == "" {
		t.Error("parent cmdline should not be empty when running from go test")
	}
}

func TestCollect_Integration(t *testing.T) {
	defaultPatterns := []string{
		"(?i)token",
		"(?i)secret",
		"(?i)password",
		"(?i)key",
		"(?i)credential",
	}

	m, err := Collect(defaultPatterns, nil)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if m.Env == nil || len(m.Env) == 0 {
		t.Error("Env should not be empty")
	}
	// Verify default patterns filter sensitive vars.
	t.Setenv("SAFERM_INTEGRATION_SECRET", "hidden")
	m2, _ := Collect(defaultPatterns, nil)
	if _, ok := m2.Env["SAFERM_INTEGRATION_SECRET"]; ok {
		t.Error("SAFERM_INTEGRATION_SECRET should be filtered by default patterns")
	}

	if m.GitBranch == "" {
		t.Error("GitBranch should not be empty in saferm repo")
	}
	if m.GitHEAD == "" {
		t.Error("GitHEAD should not be empty in saferm repo")
	}
	if m.GitRoot == "" {
		t.Error("GitRoot should not be empty in saferm repo")
	}
	if m.PPID <= 0 {
		t.Errorf("PPID should be > 0, got %d", m.PPID)
	}
	if m.ParentCmd == "" {
		t.Error("ParentCmd should not be empty")
	}
	if m.Custom != nil {
		t.Error("Custom should be nil when no custom meta is passed")
	}
}

func TestCollect_CustomMeta(t *testing.T) {
	custom := map[string]string{
		"reason": "cleanup",
		"ticket": "PROJ-123",
	}

	m, err := Collect(nil, custom)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if m.Custom == nil {
		t.Fatal("Custom should not be nil when custom meta is provided")
	}
	if len(m.Custom) != 2 {
		t.Fatalf("Custom length = %d, want 2", len(m.Custom))
	}
	if m.Custom["reason"] != "cleanup" {
		t.Errorf("Custom[reason] = %q, want 'cleanup'", m.Custom["reason"])
	}
	if m.Custom["ticket"] != "PROJ-123" {
		t.Errorf("Custom[ticket] = %q, want 'PROJ-123'", m.Custom["ticket"])
	}
}
