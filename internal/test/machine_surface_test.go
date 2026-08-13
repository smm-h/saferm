package test

import (
	"encoding/json"
	"os"
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

// `delete`'s payload is the identifier lines it already prints, in a form a
// consumer does not have to parse out of prose: one entry per archived path,
// carrying both identifiers, the path, and the size. The group identifier the
// invocation stamps on every record it writes is on the payload too, because
// nothing on the human stream ever named it.
func TestMachineSurface_DeleteNamesEveryRecordItWrote(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	first := testutil.CreateTempFile(t, workDir, "first.txt", "first\n")
	second := testutil.CreateTempFile(t, workDir, "second.txt", "second content\n")

	env, stderr, code := runSafermJSON(t, homeDir,
		"delete", "--on-error", "abort", "--description", "delete payload test", first, second)
	if code != 0 {
		t.Fatalf("delete failed (exit %d): %q", code, stderr)
	}

	var payload struct {
		GroupID  string `json:"group_id"`
		Archived []struct {
			ID   int64  `json:"id"`
			UUID string `json:"uuid"`
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"archived"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("delete's payload does not parse (%v): %s", err, env.Payload)
	}

	if len(payload.Archived) != 2 {
		t.Fatalf("both paths were archived, so both belong on the payload, got: %s", env.Payload)
	}
	if payload.GroupID == "" {
		t.Error("the payload must carry the group identifier the invocation stamped on its records")
	}
	if payload.Archived[0].Path != first || payload.Archived[1].Path != second {
		t.Errorf("the payload must name the paths in the order they were archived, got: %s", env.Payload)
	}
	if payload.Archived[0].Size != int64(len("first\n")) {
		t.Errorf("size = %d, want %d", payload.Archived[0].Size, len("first\n"))
	}
	for _, rec := range payload.Archived {
		if len(rec.UUID) != 36 {
			t.Errorf("each entry carries the durable handle, got %q", rec.UUID)
		}
		if rec.ID <= 0 {
			t.Errorf("each entry carries the database id, got %d", rec.ID)
		}
	}

	// `info` accepts what the payload handed back, which is what makes the
	// identifiers usable rather than merely present.
	if _, _, code := runSaferm(t, homeDir, "info", payload.Archived[0].UUID); code != 0 {
		t.Errorf("the payload's uuid must resolve, got exit %d", code)
	}
}

// A previewed delete produces no records, so it claims none: the envelope's
// dry_run flag and its preview say what would happen, and the payload does not
// invent identifiers for rows that were never written.
func TestMachineSurface_DeletePreviewClaimsNoRecords(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	file := testutil.CreateTempFile(t, workDir, "previewed.txt", "content\n")

	env, stderr, code := runSafermJSON(t, homeDir,
		"--dry-run", "delete", "--on-error", "abort", "--description", "delete preview payload", file)
	if code != 0 {
		t.Fatalf("previewed delete failed (exit %d): %q", code, stderr)
	}
	if !env.DryRun {
		t.Error("the envelope must report the run as a preview")
	}
	if len(env.Preview) == 0 {
		t.Error("the envelope's preview must name what the delete would do")
	}

	var payload struct {
		GroupID  string        `json:"group_id"`
		Archived []interface{} `json:"archived"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("delete's payload does not parse (%v): %s", err, env.Payload)
	}
	if len(payload.Archived) != 0 {
		t.Errorf("a preview writes no records, so it claims none, got: %s", env.Payload)
	}
	if _, err := os.Lstat(file); err != nil {
		t.Errorf("a previewed delete archived %s for real: %v", file, err)
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
