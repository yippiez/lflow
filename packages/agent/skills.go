package agent

// The embedded lflow skill teaches NLPCompute how to query the outline CLI. It
// is compiled into the binary, materialized at editor start, and passed through
// Pi's skill mechanism without changing the user's own setup.

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed skills
var skillsFS embed.FS

// AgentMaterializeSkills writes the embedded skills under dir (creating it) and
// returns the path to pass to the agent's skill flag. Files are rewritten
// every call — they are small, and this keeps an upgraded binary's skills
// current.
func AgentMaterializeSkills(dir string) (string, error) {
	err := fs.WalkDir(skillsFS, "skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, path)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := skillsFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "skills", "lflow"), nil
}
