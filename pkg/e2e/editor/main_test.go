package editor

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
)

// lflowBin is the absolute path to the lflow binary built once for the whole
// package by TestMain. Every test drives this exact binary through tmux.
var lflowBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "lflow-e2e-editor-bin")
	if err != nil {
		log.Print(errors.Wrap(err, "creating temp dir for binary").Error())
		os.Exit(1)
	}

	lflowBin = filepath.Join(tmp, "lflow")

	cmd := exec.Command("go", "build", "--tags", "fts5", "-o", lflowBin, "github.com/lflow/lflow/pkg/tui")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Print(errors.Wrap(err, "building the lflow binary").Error())
		log.Print(stderr.String())
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
