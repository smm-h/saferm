package meta

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
// Stub: not yet implemented.
func Collect() (*Metadata, error) {
	return &Metadata{}, nil
}
