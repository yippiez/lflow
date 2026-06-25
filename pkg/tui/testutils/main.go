// Package testutils provides utilities used in tests
package testutils

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/pkg/errors"
)

// Prompts for user input
const (
	PromptEmptyServer = "The server is empty but you have local data"
)

// Timeout for waiting for prompts in tests
const promptTimeout = 10 * time.Second

// CopyFixture writes the content of the given fixture to the filename inside the lflow dir
func CopyFixture(t *testing.T, ctx context.DnoteCtx, fixturePath string, filename string) {
	fp, err := filepath.Abs(fixturePath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "getting the absolute path for fixture"))
	}

	dp, err := filepath.Abs(filepath.Join(ctx.Paths.LegacyDnote, filename))
	if err != nil {
		t.Fatal(errors.Wrap(err, "getting the absolute path lflow dir"))
	}

	err = utils.CopyFile(fp, dp)
	if err != nil {
		t.Fatal(errors.Wrap(err, "copying the file"))
	}
}

// ReadFile reads the content of the file with the given name in lflow dir
func ReadFile(ctx context.DnoteCtx, filename string) []byte {
	path := filepath.Join(ctx.Paths.LegacyDnote, filename)

	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	return b
}

// ReadJSON reads JSON fixture to the struct at the destination address
func ReadJSON(path string, destination interface{}) {
	var dat []byte
	dat, err := os.ReadFile(path)
	if err != nil {
		panic(errors.Wrap(err, "Failed to load fixture payload"))
	}
	if err := json.Unmarshal(dat, destination); err != nil {
		panic(errors.Wrap(err, "Failed to get event"))
	}
}

// NewDnoteCmd returns a new Dnote command and a pointer to stderr
func NewDnoteCmd(opts RunDnoteCmdOptions, binaryName string, arg ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	var stderr, stdout bytes.Buffer

	binaryPath, err := filepath.Abs(binaryName)
	if err != nil {
		return &exec.Cmd{}, &stderr, &stdout, errors.Wrap(err, "getting the absolute path to the test binary")
	}

	cmd := exec.Command(binaryPath, arg...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	cmd.Env = opts.Env

	return cmd, &stderr, &stdout, nil
}

// RunDnoteCmdOptions is an option for RunDnoteCmd
type RunDnoteCmdOptions struct {
	Env []string
}

// RunLflowCmd runs a lflow command
func RunDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, binaryName string, arg ...string) string {
	t.Logf("running: %s %s", binaryName, strings.Join(arg, " "))

	cmd, stderr, stdout, err := NewDnoteCmd(opts, binaryName, arg...)
	if err != nil {
		t.Logf("\n%s", stdout)
		t.Fatal(errors.Wrap(err, "getting command").Error())
	}

	cmd.Env = append(cmd.Env, "DNOTE_DEBUG=1")

	if err := cmd.Run(); err != nil {
		t.Logf("\n%s", stdout)
		t.Fatal(errors.Wrapf(err, "running command %s", stderr.String()))
	}

	// Print stdout if and only if test fails later
	t.Logf("\n%s", stdout)

	return stdout.String()
}

// WaitLflowCmd runs a lflow command and passes stdout to the callback.
func WaitDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, runFunc func(io.Reader, io.WriteCloser) error, binaryName string, arg ...string) (string, error) {
	t.Logf("running: %s %s", binaryName, strings.Join(arg, " "))

	binaryPath, err := filepath.Abs(binaryName)
	if err != nil {
		return "", errors.Wrap(err, "getting absolute path to test binary")
	}

	cmd := exec.Command(binaryPath, arg...)
	cmd.Env = opts.Env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", errors.Wrap(err, "getting stdout pipe")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", errors.Wrap(err, "getting stdin")
	}
	defer stdin.Close()

	if err = cmd.Start(); err != nil {
		return "", errors.Wrap(err, "starting command")
	}

	var output bytes.Buffer
	tee := io.TeeReader(stdout, &output)

	err = runFunc(tee, stdin)
	if err != nil {
		t.Logf("\n%s", output.String())
		return output.String(), errors.Wrap(err, "running callback")
	}

	io.Copy(&output, stdout)

	if err := cmd.Wait(); err != nil {
		t.Logf("\n%s", output.String())
		return output.String(), errors.Wrapf(err, "command failed: %s", stderr.String())
	}

	t.Logf("\n%s", output.String())
	return output.String(), nil
}

func MustWaitDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, runFunc func(io.Reader, io.WriteCloser) error, binaryName string, arg ...string) string {
	output, err := WaitDnoteCmd(t, opts, runFunc, binaryName, arg...)
	if err != nil {
		t.Fatal(err)
	}

	return output
}

// UserConfirmEmptyServerSync waits for an empty server prompt and confirms.
func UserConfirmEmptyServerSync(stdout io.Reader, stdin io.WriteCloser) error {
	return assert.RespondToPrompt(stdout, stdin, PromptEmptyServer, "y\n", promptTimeout)
}

// MustMarshalJSON marshalls the given interface into JSON.
// If there is any error, it fails the test.
func MustMarshalJSON(t *testing.T, v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshalling data: %s", t.Name(), err.Error())
	}

	return b
}

// MustUnmarshalJSON marshalls the given interface into JSON.
// If there is any error, it fails the test.
func MustUnmarshalJSON(t *testing.T, data []byte, v interface{}) {
	err := json.Unmarshal(data, v)
	if err != nil {
		t.Fatalf("%s: unmarshalling data: %s", t.Name(), err.Error())
	}
}

// MustGenerateUUID generates the uuid. If error occurs, it fails the test.
func MustGenerateUUID(t *testing.T) string {
	ret, err := utils.GenerateUUID()
	if err != nil {
		t.Fatal(errors.Wrap(err, "generating uuid").Error())
	}

	return ret
}
