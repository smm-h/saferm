package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// The machine surface is what a program -- an agent, a launcher, a release tool
// -- reads instead of parsing saferm's tables. It is the framework's envelope
// with saferm's own payload inside it, and these tests hold both halves: that
// stdout carries exactly one document, and that each verb's payload says what
// the human output says.

// envelope is the framework's machine-mode document, declared here as a
// consumer sees it rather than imported: the point of the surface is that
// something outside this repository can parse it from the bytes alone.
type envelope struct {
	InterfaceVersion int                      `json:"interface_version"`
	App              string                   `json:"app"`
	AppVersion       string                   `json:"app_version"`
	Command          *string                  `json:"command"`
	ExitCode         int                      `json:"exit_code"`
	Payload          json.RawMessage          `json:"payload"`
	DryRun           bool                     `json:"dry_run"`
	Preview          []map[string]interface{} `json:"preview"`
	PreviewError     *map[string]interface{}  `json:"preview_error"`
	Diagnostics      []struct {
		Level   string `json:"level"`
		Message string `json:"message"`
	} `json:"diagnostics"`
}

// runSafermJSON runs saferm in machine mode and parses the one document stdout
// is allowed to carry. A second document, or a table printed beside the
// envelope, fails here -- which is the property the whole surface rests on.
func runSafermJSON(t *testing.T, homeDir string, args ...string) (envelope, string, int) {
	t.Helper()

	stdout, stderr, code := runSaferm(t, homeDir, append([]string{"--json"}, args...)...)

	trimmed := strings.TrimSuffix(stdout, "\n")
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("machine mode must leave exactly one document on stdout, got:\n%s", stdout)
	}
	var env envelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		t.Fatalf("stdout is not one envelope (%v): %q", err, stdout)
	}
	if env.InterfaceVersion != 1 {
		t.Errorf("interface_version = %d, want 1", env.InterfaceVersion)
	}
	if env.App != "saferm" {
		t.Errorf("app = %q, want saferm", env.App)
	}
	if env.ExitCode != code {
		t.Errorf("the envelope reports exit_code %d, the process exited %d", env.ExitCode, code)
	}
	return env, stderr, code
}

// Every verb's human output goes through the context writers, so machine mode
// carries it in the envelope's diagnostics instead of printing it beside the
// envelope. A table on stdout next to the document is the one thing a consumer
// cannot recover from, so it is asserted for every verb that prints one --
// including `purge`, which has no payload and is outside the machine surface.
func TestMachineMode_StdoutCarriesOnlyTheEnvelope(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	file := testutil.CreateTempFile(t, workDir, "one.txt", "content\n")
	stdout, stderr, code := runSaferm(t, homeDir, "delete", "--on-error", "abort", "--description", "machine mode test", file)
	if code != 0 {
		t.Fatalf("delete failed (exit %d): stderr=%q", code, stderr)
	}
	uuid := parseArchivedUUID(t, stdout)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list", []string{"list"}},
		{"info", []string{"info", uuid}},
		{"purge preview", []string{"--dry-run", "purge", "--all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stderr, code := runSafermJSON(t, homeDir, tc.args...)
			if code != 0 {
				t.Fatalf("%v failed (exit %d): %q", tc.args, code, stderr)
			}
			// The text the run would have printed is not lost: it rides the
			// envelope.
			var joined strings.Builder
			for _, d := range env.Diagnostics {
				joined.WriteString(d.Message)
			}
			if !strings.Contains(joined.String(), "one.txt") {
				t.Errorf("the envelope's diagnostics must carry what the run printed, got: %q", joined.String())
			}
		})
	}
}

// A machine-mode run under --quiet still emits the complete envelope: the
// document is not written through the writers --quiet suppresses, so quiet has
// no mechanism by which to reach it.
func TestMachineMode_QuietDoesNotReachTheEnvelope(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	file := testutil.CreateTempFile(t, workDir, "quiet.txt", "content\n")
	if _, stderr, code := runSaferm(t, homeDir, "delete", "--on-error", "abort", "--description", "quiet machine test", file); code != 0 {
		t.Fatalf("delete failed (exit %d): stderr=%q", code, stderr)
	}

	env, stderr, code := runSafermJSON(t, homeDir, "--quiet", "list")
	if code != 0 {
		t.Fatalf("a quiet machine-mode list failed (exit %d): %q", code, stderr)
	}
	if env.Command == nil || *env.Command != "list" {
		t.Errorf("the envelope must name the command it ran, got: %v", env.Command)
	}
}
