package sync

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pkg/errors"
)

var cliBinaryName string
var serverTime = time.Date(2017, time.March, 14, 21, 15, 0, 0, time.UTC)

var testDir = "./tmp/"

func init() {
	cliBinaryName = fmt.Sprintf("%s/test-cli", testDir)
}

func TestMain(m *testing.M) {
	// Build CLI binary without hardcoded API endpoint
	// Each test will create its own server and config file
	cmd := exec.Command("go", "build", "--tags", "fts5", "-o", cliBinaryName, "github.com/lflow/lflow/pkg/cli")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Print(errors.Wrap(err, "building a CLI binary").Error())
		log.Print(stderr.String())
		os.Exit(1)
	}

	os.Exit(m.Run())
}
