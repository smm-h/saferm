package meta

import (
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/smm-h/saferm/internal/config"
)

// Metadata holds contextual information captured at deletion time.
type Metadata struct {
	Env       map[string]string `json:"env,omitempty"`
	GitBranch string            `json:"git_branch,omitempty"`
	GitHEAD   string            `json:"git_head,omitempty"`
	GitRoot   string            `json:"git_root,omitempty"`
	PPID      int               `json:"ppid,omitempty"`
	ParentCmd string            `json:"parent_cmd,omitempty"`
	Custom    map[string]string `json:"custom,omitempty"`
}

// Collect gathers metadata from the current environment.
// Never fails fatally -- if a collector errors, it populates what it can.
func Collect(cfg *config.Config, customMeta map[string]string) (*Metadata, error) {
	m := &Metadata{}

	m.Env = collectEnv(cfg.ExcludeEnvPatterns)
	m.GitBranch, m.GitHEAD, m.GitRoot = collectGitContext()
	m.PPID, m.ParentCmd = collectParentProcess()

	if len(customMeta) > 0 {
		m.Custom = make(map[string]string, len(customMeta))
		for k, v := range customMeta {
			m.Custom[k] = v
		}
	}

	return m, nil
}

// collectEnv reads all environment variables and filters out those whose
// names match any of the exclude patterns.
func collectEnv(excludePatterns []string) map[string]string {
	compiled := make([]*regexp.Regexp, 0, len(excludePatterns))
	for _, pat := range excludePatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		compiled = append(compiled, re)
	}

	env := os.Environ()
	result := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		excluded := false
		for _, re := range compiled {
			if re.MatchString(name) {
				excluded = true
				break
			}
		}
		if !excluded {
			result[name] = value
		}
	}
	return result
}

// collectGitContext retrieves git branch, HEAD SHA, and repo root.
// Returns empty strings if not in a git repository.
func collectGitContext() (branch, head, root string) {
	branch = runGitCmd("rev-parse", "--abbrev-ref", "HEAD")
	head = runGitCmd("rev-parse", "HEAD")
	root = runGitCmd("rev-parse", "--show-toplevel")
	return
}

// runGitCmd executes a git command and returns trimmed stdout, or empty string on failure.
func runGitCmd(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// collectParentProcess returns the PPID and the parent's command line.
func collectParentProcess() (ppid int, cmdline string) {
	ppid = os.Getppid()
	cmdline = readParentCmdline(ppid)
	return
}
